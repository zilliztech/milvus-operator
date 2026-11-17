import pathlib
import subprocess
import tempfile
import unittest


ROOT = pathlib.Path(__file__).resolve().parents[1]
CHART = ROOT / "charts" / "milvus-operator"
VALUES = """\
volumes:
  - name: custom
    emptyDir: {}
volumeMounts:
  - name: custom
    mountPath: /custom
"""


def render(*args):
    with tempfile.TemporaryDirectory() as directory:
        values = pathlib.Path(directory) / "values.yaml"
        values.write_text(VALUES, encoding="utf-8")
        return subprocess.check_output(
            ["helm", "template", "milvus-operator", str(CHART), "-f", str(values), *args],
            cwd=ROOT,
            text=True,
        )


class HelmChartVolumesTest(unittest.TestCase):
    def test_custom_volumes_and_mounts(self):
        rendered = render()
        self.assertIn("mountPath: /custom", rendered)
        self.assertIn("name: custom", rendered)

    def test_webhook_certificates_coexist_with_custom_volumes(self):
        rendered = render("--set", "enableWebhook=true")
        self.assertIn("mountPath: /tmp/k8s-webhook-server/serving-certs", rendered)
        self.assertIn("mountPath: /custom", rendered)


if __name__ == "__main__":
    unittest.main()
