"""Regression tests for false-positive convergence and cleanup failure handling."""
import copy
import sys
import types
import importlib.util
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("lifecycle", Path(__file__).with_name("cluster_lifecycle.py"))
lifecycle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lifecycle)


class ConvergenceTest(unittest.TestCase):
    def setUp(self):
        self.cr = {"metadata": {"generation": 4}, "status": {
            "observedGeneration": 4, "conditions": [
                {"type": c, "status": "True"} for c in ("MilvusReady", "MilvusUpdated")]}}
        self.workloads, self.pods = [], []
        for component in ("proxy", "querynode"):
            self.workloads.append({"metadata": {"generation": 2, "labels": {lifecycle.COMPONENT: component}},
                                   "spec": {"replicas": 1}, "status": {
                                       "observedGeneration": 2, "replicas": 1, "updatedReplicas": 1,
                                       "readyReplicas": 1, "availableReplicas": 1}})
            self.pods.append({"metadata": {"uid": component, "labels": {lifecycle.COMPONENT: component},
                                           "annotations": {lifecycle.MARKER: "new"}},
                              "spec": {"containers": [{"name": component, "image": "new-image"}]},
                              "status": {"conditions": [{"type": "Ready", "status": "True"}]}})

    def check(self, **kwargs):
        return lifecycle.converged(self.cr, self.workloads, self.pods, "new-image",
                                   {"proxy": 1, "querynode": 1}, **kwargs)

    def test_success_with_idle_blue_green_slot(self):
        idle = copy.deepcopy(self.workloads[1])
        idle["spec"]["replicas"] = 0
        idle["status"] = {"observedGeneration": 2}
        self.workloads.append(idle)
        self.assertTrue(self.check(marker="new", previous={"old-uid"}))

    def test_stale_cr_ready(self):
        self.cr["status"]["observedGeneration"] = 3
        self.assertFalse(self.check())

    def test_not_updated(self):
        self.cr["status"]["conditions"][1]["status"] = "False"
        self.assertFalse(self.check())

    def test_empty_resources(self):
        self.workloads.clear()
        self.assertFalse(self.check())

    def test_stale_workload(self):
        self.workloads[0]["status"]["observedGeneration"] = 1
        self.assertFalse(self.check())

    def test_old_replicas(self):
        self.workloads[0]["status"]["updatedReplicas"] = 0
        self.assertFalse(self.check())

    def test_scale_not_applied(self):
        self.workloads[0]["spec"]["replicas"] = 2
        for key in ("replicas", "updatedReplicas", "readyReplicas", "availableReplicas"):
            self.workloads[0]["status"][key] = 2
        self.assertFalse(self.check())

    def test_terminating_pod(self):
        self.pods[0]["metadata"]["deletionTimestamp"] = "now"
        self.assertFalse(self.check())

    def test_old_uid(self):
        self.assertFalse(self.check(previous={"querynode"}))

    def test_old_marker(self):
        self.assertFalse(self.check(marker="different"))

    def test_old_image(self):
        self.pods[0]["spec"]["containers"][0]["image"] = "old-image"
        self.assertFalse(self.check())

    def test_not_ready(self):
        self.pods[0]["status"]["conditions"][0]["status"] = "False"
        self.assertFalse(self.check())

    def test_extra_pod_after_scale_down(self):
        self.pods.append(copy.deepcopy(self.pods[1]))
        self.assertFalse(self.check())

    def test_missing_component_pod(self):
        self.pods.pop()
        self.assertFalse(self.check())


class RunnerTest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        env = patch.dict("os.environ", {"SIT_CONTEXT": "test", "FROM_IMAGE": "old", "TO_IMAGE": "new",
                                       "ARTIFACT_DIR": self.tmp.name})
        env.start()
        self.addCleanup(env.stop)
        self.runner = lifecycle.Runner()

    def test_preserves_failure_and_cleans_up(self):
        self.runner.owned = True
        self.runner.exercise = Mock(side_effect=RuntimeError("original"))
        self.runner.diagnostics = Mock()
        self.runner.cleanup = Mock(side_effect=RuntimeError("cleanup"))
        with self.assertRaisesRegex(RuntimeError, "original"):
            self.runner.run()
        self.runner.diagnostics.assert_called_once()
        self.runner.cleanup.assert_called_once()

    def test_cleanup_failure_fails_successful_test(self):
        self.runner.owned = True
        self.runner.exercise = Mock()
        self.runner.diagnostics = Mock()
        self.runner.cleanup = Mock(side_effect=RuntimeError("cleanup"))
        with self.assertRaisesRegex(RuntimeError, "cleanup"):
            self.runner.run()

    def test_never_cleans_namespace_it_did_not_create(self):
        self.runner.exercise = Mock(side_effect=RuntimeError("namespace create failed"))
        self.runner.cleanup = Mock()
        with self.assertRaisesRegex(RuntimeError, "namespace create failed"):
            self.runner.run()
        self.runner.cleanup.assert_not_called()

    def test_namespace_cleanup_attempted_after_cr_delete_failure(self):
        self.runner.owned = True
        self.runner.get = Mock(return_value={"items": []})
        self.runner.kubectl = Mock(side_effect=[RuntimeError("finalizer"), "", ""])
        with self.assertRaisesRegex(RuntimeError, "finalizer"):
            self.runner.cleanup()
        self.assertEqual(self.runner.kubectl.call_args_list[1].args[:2], ("delete", "namespace"))

    def test_failed_data_job_fails_immediately(self):
        self.runner.create = Mock()
        self.runner.get = Mock(return_value={"status": {"failed": 1}})
        self.runner.capture = Mock()
        with self.assertRaisesRegex(RuntimeError, "data job verify-2 failed"):
            self.runner.data_check(2)
        self.runner.capture.assert_called_once()

    def test_timeout_is_not_success(self):
        self.runner.timeout = 0
        with self.assertRaises(TimeoutError):
            self.runner.wait("never ready", lambda: False)


    def test_successful_exercise_orders_all_operations(self):
        self.runner.create = Mock()
        self.runner.kubectl = Mock(return_value='{"spec": {"components": {}}}')
        self.runner.pods = Mock(return_value=[{"metadata": {"uid": "old"}}])
        self.runner.patch = Mock()
        self.runner.settle = Mock()
        self.runner.data_check = Mock()
        self.runner.exercise()
        self.assertTrue(self.runner.owned)
        self.assertEqual([c.args for c in self.runner.data_check.call_args_list],
                         [(0,), (0,), (1,), (2,), (3,), (4,), (5,), (6,)])
        self.assertEqual(self.runner.patch.call_args_list[0].args, ({"image": "new"},))
        self.assertEqual([c.args for c in self.runner.patch.call_args_list[2:]], [
            ({"proxy": {"replicas": 2}},), ({"queryNode": {"replicas": 2}},),
            ({"proxy": {"replicas": 1}},), ({"queryNode": {"replicas": 1}},)])
        for call in self.runner.settle.call_args_list[1:3]:
            self.assertEqual(call.args[2], {"old"})

    def test_cleanup_waits_for_volume_deletion(self):
        self.runner.owned = True
        self.runner.get = Mock(return_value={"items": [{"spec": {"volumeName": "test-pv"}}]})
        self.runner.kubectl = Mock(return_value="")
        self.runner.cleanup()
        self.assertEqual(self.runner.kubectl.call_args.args[:3], ("get", "pv", "test-pv"))

    def test_cleanup_detects_namespace_leftover(self):
        self.runner.owned = True
        self.runner.get = Mock(return_value={"items": []})
        self.runner.kubectl = Mock(side_effect=["", "", "namespace/stuck"])
        with self.assertRaisesRegex(RuntimeError, "Namespace remains"):
            self.runner.cleanup()

    def test_diagnostics_failure_still_cleans_up(self):
        self.runner.owned = True
        self.runner.exercise = Mock()
        self.runner.diagnostics = Mock(side_effect=OSError("disk full"))
        self.runner.cleanup = Mock()
        with self.assertRaisesRegex(OSError, "disk full"):
            self.runner.run()
        self.runner.cleanup.assert_called_once()

    def test_subprocess_error_is_not_silently_ignored(self):
        with patch.object(lifecycle.subprocess, "run", return_value=Mock(returncode=1, stderr="denied")) as run:
            with self.assertRaisesRegex(RuntimeError, "denied"):
                self.runner.create({"kind": "Job"})
            self.assertEqual(run.call_args.args[0][1:3], ["--context", "test"])

    def test_successful_json_get(self):
        with patch.object(lifecycle.subprocess, "run", return_value=Mock(returncode=0, stdout='{"items": []}')):
            self.assertEqual(self.runner.pods(), [])

    def test_capture_keeps_error_as_diagnostic(self):
        self.runner.kubectl = Mock(side_effect=RuntimeError("log unavailable"))
        self.runner.capture("test.log", "logs", "pod")
        self.assertIn("log unavailable", (self.runner.artifacts / "test.log").read_text())

    def test_diagnostics_collects_pod_logs(self):
        self.runner.capture = Mock()
        self.runner.get = Mock(return_value={"items": [{"metadata": {"name": "test-pod"}}]})
        self.runner.diagnostics()
        self.assertIn("test-pod.log", [c.args[0] for c in self.runner.capture.call_args_list])

    def test_settle_saves_snapshot(self):
        self.runner.wait = Mock()
        self.runner.get = Mock(return_value={"metadata": {}})
        self.runner.pods = Mock(return_value=[])
        self.runner.settle("new", {"proxy": 1})
        self.assertEqual(len(list(self.runner.artifacts.glob("state-*.json"))), 1)

    def test_successful_job_logs_are_saved(self):
        self.runner.create = Mock()
        self.runner.get = Mock(return_value={"status": {"succeeded": 1}})
        self.runner.kubectl = Mock(return_value="PASS stage=1")
        self.runner.data_check(1)
        self.assertEqual((self.runner.artifacts / "verify-1.log").read_text(), "PASS stage=1")

    def test_probe_is_nonblocking(self):
        self.runner.create = Mock()
        self.runner.wait = Mock()
        self.runner.data_check(0, probe=True)
        self.runner.wait.assert_not_called()
        command = self.runner.create.call_args.args[0]["spec"]["template"]["spec"]["containers"][0]["command"]
        self.assertIn("--probe", command)

    def test_identical_versions_rejected(self):
        with patch.dict("os.environ", {"FROM_IMAGE": "new"}):
            with self.assertRaisesRegex(ValueError, "must differ"):
                lifecycle.Runner()



class DataAssertionsTest(unittest.TestCase):
    def setUp(self):
        sdk = types.ModuleType("pymilvus")
        for name in ("Collection", "CollectionSchema", "DataType", "FieldSchema", "connections", "utility"):
            setattr(sdk, name, Mock())
        spec = importlib.util.spec_from_file_location("data_check", Path(__file__).with_name("cluster-lifecycle.py"))
        self.data = importlib.util.module_from_spec(spec)
        with patch.dict(sys.modules, {"pymilvus": sdk}):
            spec.loader.exec_module(self.data)
        self.sdk = sdk
        self.collection = Mock()
        self.sdk.Collection.return_value = self.collection

    def valid_results(self):
        self.collection.query.return_value = [{"id": pk, "value": pk * 7} for pk in range(self.data.BATCH)]
        self.collection.search.return_value = [[Mock(id=pk, distance=0.0)] for pk in (0, self.data.BATCH - 1)]

    def test_exact_persisted_data_passes(self):
        self.valid_results()
        self.data.verify(self.collection, 1)
        self.assertEqual(self.data.vector(12), self.data.vector(12))

    def test_missing_primary_key_fails(self):
        self.valid_results()
        self.collection.query.return_value.pop()
        with self.assertRaisesRegex(AssertionError, "persisted data mismatch"):
            self.data.verify(self.collection, 1)

    def test_corrupt_scalar_fails(self):
        self.valid_results()
        self.collection.query.return_value[4]["value"] = -1
        with self.assertRaisesRegex(AssertionError, "persisted data mismatch"):
            self.data.verify(self.collection, 1)

    def test_empty_search_fails(self):
        self.valid_results()
        self.collection.search.return_value = []
        with self.assertRaisesRegex(AssertionError, "missing search results"):
            self.data.verify(self.collection, 1)

    def test_wrong_search_id_fails(self):
        self.valid_results()
        self.collection.search.return_value[0][0].id = 99
        with self.assertRaisesRegex(AssertionError, "search failed"):
            self.data.verify(self.collection, 1)

    def run_stage(self, stage, *extra):
        with patch.object(sys, "argv", ["check.py", str(stage), "--host", "milvus", *extra]):
            self.data.main()

    def test_seed_creates_loads_and_verifies(self):
        self.sdk.utility.has_collection.return_value = False
        self.collection.insert.return_value.primary_keys = list(range(self.data.BATCH))
        with patch.object(self.data, "verify") as verify:
            self.run_stage(0)
        self.collection.create_index.assert_called_once()
        self.collection.load.assert_called_once()
        verify.assert_called_once_with(self.collection, 1)

    def test_later_stage_preserves_load_and_checks_old_data_first(self):
        self.sdk.utility.has_collection.return_value = True
        self.collection.insert.return_value.primary_keys = list(range(self.data.BATCH, 2 * self.data.BATCH))
        with patch.object(self.data, "verify") as verify:
            self.run_stage(1)
        self.assertEqual([c.args[1] for c in verify.call_args_list], [1, 2])
        self.collection.load.assert_not_called()
        self.collection.create_index.assert_not_called()

    def test_missing_collection_is_failure_not_recreation(self):
        self.sdk.utility.has_collection.return_value = False
        with self.assertRaisesRegex(AssertionError, "original collection is missing"):
            self.run_stage(1)
        self.sdk.Collection.assert_not_called()

    def test_existing_seed_collection_rejected(self):
        self.sdk.utility.has_collection.return_value = True
        with self.assertRaisesRegex(AssertionError, "already exists"):
            self.run_stage(0)

    def test_bad_insert_result_rejected(self):
        self.sdk.utility.has_collection.return_value = False
        self.collection.insert.return_value.primary_keys = []
        with self.assertRaisesRegex(AssertionError, "insert primary keys mismatch"):
            self.run_stage(0)

    def test_probe_records_failure_and_recovery(self):
        with patch.object(self.data, "verify", side_effect=[RuntimeError("unavailable"), None]), \
                patch.object(self.data.time, "sleep", side_effect=[None, KeyboardInterrupt]), \
                patch("builtins.print") as log:
            with self.assertRaises(KeyboardInterrupt):
                self.run_stage(0, "--probe")
        import json
        first, second = [json.loads(c.args[0]) for c in log.call_args_list]
        self.assertEqual(first["failures"], 1)
        self.assertEqual(second["successes"], 1)
        self.assertEqual(second["failures"], 1)


if __name__ == "__main__":
    unittest.main()
