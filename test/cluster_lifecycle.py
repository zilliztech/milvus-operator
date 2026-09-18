"""Cluster lifecycle SIT runner. Uses kubectl JSON and Python's standard library."""
import json
import os
from pathlib import Path
import signal
import subprocess
import time
import uuid

COMPONENT = "app.kubernetes.io/component"
MARKER = "sit.milvus.io/rollout"
SELECTOR = "app.kubernetes.io/name=milvus,app.kubernetes.io/instance=lifecycle"


def converged(cr, workloads, pods, image, counts, previous=(), marker=None):
    status = cr.get("status", {})
    if status.get("observedGeneration", 0) < cr["metadata"]["generation"]:
        return False
    conditions = {c["type"]: c["status"] for c in status.get("conditions", [])}
    if any(conditions.get(c) != "True" for c in ("MilvusReady", "MilvusUpdated")):
        return False
    if not workloads or not pods:
        return False
    desired = {}
    for workload in workloads:
        component = workload["metadata"]["labels"][COMPONENT]
        spec, state = workload["spec"], workload.get("status", {})
        replicas = spec.get("replicas", 1)
        if state.get("observedGeneration", 0) < workload["metadata"]["generation"]:
            return False
        if any(state.get(k, 0) != replicas for k in ("replicas", "updatedReplicas", "readyReplicas", "availableReplicas")):
            return False
        desired[component] = desired.get(component, 0) + replicas
    if any(desired.get(c) != n for c, n in counts.items()):
        return False
    actual = {}
    for pod in pods:
        meta = pod["metadata"]
        component = meta["labels"][COMPONENT]
        if meta.get("deletionTimestamp") or meta["uid"] in previous:
            return False
        if marker and meta.get("annotations", {}).get(MARKER) != marker:
            return False
        containers = [c for c in pod["spec"]["containers"] if c["name"] == component]
        if len(containers) != 1 or containers[0]["image"] != image:
            return False
        if not any(c["type"] == "Ready" and c["status"] == "True"
                   for c in pod.get("status", {}).get("conditions", [])):
            return False
        actual[component] = actual.get(component, 0) + 1
    return actual == {c: n for c, n in desired.items() if n}


class Runner:
    def __init__(self):
        self.context = os.environ["SIT_CONTEXT"]
        self.source, self.target = os.environ["FROM_IMAGE"], os.environ["TO_IMAGE"]
        if self.source == self.target:
            raise ValueError("FROM_IMAGE and TO_IMAGE must differ")
        self.namespace = "milvus-lifecycle-" + uuid.uuid4().hex[:10]
        self.artifacts = Path(os.environ.get("ARTIFACT_DIR", "artifacts/cluster-lifecycle")) / self.namespace
        self.artifacts.mkdir(parents=True, exist_ok=True)
        self.owned = False
        self.timeout = int(os.environ.get("SIT_TIMEOUT", "1200"))
        self.sdk_image = os.environ.get("SDK_IMAGE", "bitnamilegacy/pymilvus:2.4.6")

    def kubectl(self, *args, data=None, timeout=90):
        cmd = ["kubectl", "--context", self.context, "--request-timeout=60s", "-n", self.namespace, *args]
        result = subprocess.run(cmd, input=json.dumps(data) if data is not None else None,
                                text=True, capture_output=True, timeout=timeout)
        if result.returncode:
            raise RuntimeError(f"{' '.join(cmd)}: {result.stderr}")
        return result.stdout

    def get(self, resource, *args):
        return json.loads(self.kubectl("get", resource, *args, "-o", "json"))

    def create(self, obj):
        return self.kubectl("create", "-f", "-", data=obj)

    def patch(self, components):
        self.kubectl("patch", "milvus", "lifecycle", "--type=merge", "-p",
                     json.dumps({"spec": {"components": components}}))

    def wait(self, description, check):
        print(f"WAIT {description}", flush=True)
        deadline = time.monotonic() + self.timeout
        while time.monotonic() < deadline:
            if check():
                print(f"PASS {description}", flush=True)
                return
            time.sleep(5)
        raise TimeoutError(description)

    def pods(self):
        return self.get("pods", "-l", SELECTOR)["items"]

    def settle(self, image, counts, previous=(), marker=None):
        self.wait(f"convergence image={image} replicas={counts}", lambda: converged(
            self.get("milvus", "lifecycle"),
            self.get("deployments", "-l", SELECTOR)["items"], self.pods(),
            image, counts, previous, marker))
        snapshot = {"milvus": self.get("milvus", "lifecycle"), "pods": self.pods()}
        (self.artifacts / f"state-{time.time_ns()}.json").write_text(json.dumps(snapshot, indent=2))

    def data_check(self, stage, probe=False):
        name = "availability-probe" if probe else f"verify-{stage}"
        self.create({"apiVersion": "batch/v1", "kind": "Job", "metadata": {"name": name}, "spec": {
            "backoffLimit": 0, "activeDeadlineSeconds": 3600 if probe else 600,
            "template": {"spec": {"restartPolicy": "Never", "nodeSelector": {"kubernetes.io/arch": "amd64"}, "containers": [{
                "name": "verify", "image": self.sdk_image,
                "command": ["python3", "/scripts/check.py", str(stage), "--host", "lifecycle-milvus"] + (["--probe"] if probe else []),
                "volumeMounts": [{"name": "scripts", "mountPath": "/scripts"}],
            }], "volumes": [{"name": "scripts", "configMap": {"name": "lifecycle-check"}}]}}}})

        if probe:
            return

        def finished():
            state = self.get("job", name).get("status", {})
            if state.get("failed", 0) or any(c["type"] == "Failed" and c["status"] == "True"
                                             for c in state.get("conditions", [])):
                raise RuntimeError(f"data job {name} failed")
            return state.get("succeeded", 0) == 1
        try:
            self.wait(f"data stage={stage}", finished)
        finally:
            self.capture(f"{name}.log", "logs", f"job/{name}", "--all-containers=true")

    def capture(self, filename, *args):
        try:
            output = self.kubectl(*args)
        except Exception as exc:
            output = str(exc)
        (self.artifacts / filename).write_text(output)

    def diagnostics(self):
        self.capture("resources.yaml", "get", "milvus,deploy,sts,rs,pods,jobs,svc,pvc", "-o", "yaml")
        self.capture("events.txt", "get", "events", "--sort-by=.metadata.creationTimestamp")
        self.capture("describe-pods.txt", "describe", "pods")
        self.capture("operator.yaml", "-n", "milvus-operator", "get", "deploy/milvus-operator", "-o", "yaml")
        self.capture("operator.log", "-n", "milvus-operator", "logs", "deploy/milvus-operator", "--tail=2000")
        try:
            for pod in self.get("pods")["items"]:
                name = pod["metadata"]["name"]
                self.capture(name + ".log", "logs", name, "--all-containers=true", "--tail=1000")
        except Exception as exc:
            print(f"Diagnostics incomplete: {exc}", flush=True)

    def cleanup(self):
        if not self.owned:
            return
        errors = []
        volumes = []
        try:
            volumes = [p["spec"]["volumeName"] for p in self.get("pvc")["items"]
                       if p.get("spec", {}).get("volumeName")]
        except Exception as exc:
            errors.append(f"Cannot inventory test volumes: {exc}")
        # Attempt all cleanup steps even if the operator's CR finalizer fails.
        for args in [
            ("delete", "milvus", "lifecycle", "--ignore-not-found", "--timeout=300s"),
            ("delete", "namespace", self.namespace, "--ignore-not-found", "--timeout=300s"),
        ]:
            try:
                self.kubectl(*args, timeout=330)
            except Exception as exc:
                errors.append(str(exc))
        remaining = self.kubectl(
            "get", "namespace", self.namespace, "--ignore-not-found", "-o", "name")
        if remaining.strip():
            errors.append(f"Namespace remains: {remaining}")
        if errors:
            raise RuntimeError("Cleanup failed: " + "\n".join(errors))
        if volumes:
            self.wait("test PV deletion", lambda: not self.kubectl(
                "get", "pv", *volumes, "--ignore-not-found", "-o", "name").strip())
        print(f"CLEANED {self.namespace} volumes={volumes}", flush=True)

    def exercise(self):
        print(f"context={self.context} namespace={self.namespace} {self.source} -> {self.target}", flush=True)
        self.create({"apiVersion": "v1", "kind": "Namespace", "metadata": {
            "name": self.namespace, "labels": {"sit.milvus.io/test": "cluster-lifecycle"}}})
        self.owned = True
        manifest = json.loads(self.kubectl("create", "--dry-run=client", "-f", "test/cluster-lifecycle.yaml", "-o", "json"))
        manifest["spec"]["components"]["image"] = self.source
        manifest["spec"]["components"]["rollingMode"] = int(os.environ.get("ROLLING_MODE", "2"))
        if os.environ.get("MILVUS_NODE_ARCH"):
            manifest["spec"]["components"]["nodeSelector"] = {"kubernetes.io/arch": os.environ["MILVUS_NODE_ARCH"]}
        self.create(manifest)
        self.create({"apiVersion": "v1", "kind": "ConfigMap", "metadata": {"name": "lifecycle-check"},
                     "data": {"check.py": Path("test/cluster-lifecycle.py").read_text()}})
        counts = {"proxy": 1, "querynode": 1}
        self.settle(self.source, counts)
        self.data_check(0)
        self.data_check(0, probe=True)
        previous = {p["metadata"]["uid"] for p in self.pods()}
        self.patch({"image": self.target})
        self.settle(self.target, counts, previous)
        self.data_check(1)
        previous = {p["metadata"]["uid"] for p in self.pods()}
        marker = uuid.uuid4().hex
        self.patch({"podAnnotations": {MARKER: marker}})
        self.settle(self.target, counts, previous, marker)
        self.data_check(2)
        for stage, (field, component, replicas) in enumerate([
            ("proxy", "proxy", 2), ("queryNode", "querynode", 2),
            ("proxy", "proxy", 1), ("queryNode", "querynode", 1),
        ], start=3):
            print(f"SCALE {component} -> {replicas}", flush=True)
            self.patch({field: {"replicas": replicas}})
            counts[component] = replicas
            self.settle(self.target, counts, marker=marker)
            self.data_check(stage)

    def run(self):
        failure = None
        try:
            self.exercise()
        except BaseException as exc:
            failure = exc
        finally:
            if self.owned:
                try:
                    self.diagnostics()
                except Exception as exc:
                    print(f"Diagnostics failed: {exc}", flush=True)
                    failure = failure or exc
                try:
                    self.cleanup()
                except Exception as exc:
                    print(str(exc), flush=True)
                    failure = failure or exc
        if failure:
            raise failure


def interrupt(signum, _frame):
    raise KeyboardInterrupt(f"signal {signum}")


if __name__ == "__main__":
    signal.signal(signal.SIGTERM, interrupt)
    Runner().run()
