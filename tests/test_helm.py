"""Render the chart and verify resource contracts. Requires Helm and PyYAML."""
import os
from pathlib import Path
import subprocess
import shutil
import tempfile
import unittest

import yaml

ROOT = Path(__file__).resolve().parents[1]
CHART = ROOT / 'deploy/helm/kube-dra-exporter'
HELM = os.environ.get('HELM', 'helm')


def render(values=None, production=False, expect_error=None, chart_dir=CHART):
    args = [HELM, 'template', 'regression', str(chart_dir), '--namespace', 'exporter']
    if production:
        args += ['-f', str(chart_dir / 'prod-values.yaml')]
    result = subprocess.run(args + ['-f', '-'], input=yaml.safe_dump(values or {}),
                            text=True, capture_output=True)
    if expect_error:
        assert result.returncode != 0, 'invalid settings rendered successfully'
        assert expect_error in result.stderr, result.stderr
        return
    if result.returncode:
        raise AssertionError(result.stderr)
    return [doc for doc in yaml.safe_load_all(result.stdout) if doc]


def resource(docs, kind):
    matches = [doc for doc in docs if doc['kind'] == kind]
    assert len(matches) == 1, f'Expected one {kind}, found {len(matches)}'
    return matches[0]


class HelmTests(unittest.TestCase):
    def test_defaults_and_liveness(self):
        docs = render()
        self.assertFalse(any(doc['kind'] in ('NetworkPolicy', 'PodDisruptionBudget') for doc in docs))
        deployment = resource(docs, 'Deployment')
        container = deployment['spec']['template']['spec']['containers'][0]
        self.assertNotIn('args', container)
        self.assertEqual(container['livenessProbe']['httpGet'], {'path': '/healthz', 'port': 'http', 'scheme': 'HTTP'})
        self.assertEqual(deployment['spec']['strategy']['type'], 'RollingUpdate')

    def test_image_follows_app_version(self):
        with tempfile.TemporaryDirectory() as directory:
            chart_dir = Path(directory) / 'chart'
            shutil.copytree(CHART, chart_dir)
            metadata = yaml.safe_load((chart_dir / 'Chart.yaml').read_text())
            metadata['appVersion'] = '9.8.7'
            (chart_dir / 'Chart.yaml').write_text(yaml.safe_dump(metadata))
            for production in (False, True):
                docs = render(production=production, chart_dir=chart_dir)
                image = resource(docs, 'Deployment')['spec']['template']['spec']['containers'][0]['image']
                self.assertEqual(image, 'ghcr.io/gradiant/kube-dra-exporter:9.8.7')
            docs = render({'image': {'repository': 'example/exporter', 'tag': 'custom'}}, chart_dir=chart_dir)
            image = resource(docs, 'Deployment')['spec']['template']['spec']['containers'][0]['image']
            self.assertEqual(image, 'example/exporter:custom')

    def test_monitors_use_actual_port(self):
        for monitor, kind in [('podmonitor', 'PodMonitor'), ('servicemonitor', 'ServiceMonitor')]:
            with self.subTest(monitor=monitor):
                docs = render({'prometheus': {monitor: {'enabled': True, 'namespace': 'monitoring'}}})
                obj = resource(docs, kind)
                container = resource(docs, 'Deployment')['spec']['template']['spec']['containers'][0]
                endpoint = obj['spec']['podMetricsEndpoints' if monitor == 'podmonitor' else 'endpoints'][0]
                self.assertIn(endpoint['port' if monitor == 'podmonitor' else 'targetPort'], [port['name'] for port in container['ports']])
                self.assertEqual(obj['spec']['namespaceSelector']['matchNames'], ['exporter'])
        render({'prometheus': {'podmonitor': {'enabled': True}, 'servicemonitor': {'enabled': True}}}, expect_error='not both')

    def test_cluster_rbac(self):
        docs = render()
        role = resource(docs, 'ClusterRole')
        self.assertEqual(role['rules'][0], {'apiGroups': ['resource.k8s.io'], 'resources': ['resourceclaims', 'resourceslices', 'deviceclasses'], 'verbs': ['list']})
        binding = resource(docs, 'ClusterRoleBinding')
        self.assertNotIn('namespace', binding['metadata'])
        self.assertEqual(binding['roleRef']['name'], role['metadata']['name'])
        self.assertEqual(binding['roleRef']['kind'], 'ClusterRole')
        self.assertEqual(binding['subjects'][0]['namespace'], 'exporter')
        render({'rbac': {'useClusterRole': False}}, expect_error='cluster-wide list permissions')
        docs = render({'rbac': {'create': False}})
        self.assertFalse(any(doc['kind'] in ('ClusterRole', 'ClusterRoleBinding', 'Role', 'RoleBinding') for doc in docs))
        docs = render({'rbac': {'useExistingRole': 'existing'}})
        self.assertFalse(any(doc['kind'] == 'ClusterRole' for doc in docs))
        self.assertEqual(resource(docs, 'ClusterRoleBinding')['roleRef']['name'], 'existing')

    def test_pdb(self):
        for limits, expected in [({}, {'minAvailable': 1}), ({'minAvailable': 0}, {'minAvailable': 0}), ({'maxUnavailable': 0}, {'maxUnavailable': 0}), ({'minAvailable': '50%'}, {'minAvailable': '50%'}), ({'maxUnavailable': 1}, {'maxUnavailable': 1})]:
            with self.subTest(limits=limits):
                docs = render({'replicaCount': 3, 'podDisruptionBudget': {'enabled': True, **limits}, 'namespace': 'custom', 'fullnameOverride': 'custom-exporter'})
                pdb = resource(docs, 'PodDisruptionBudget')
                deployment = resource(docs, 'Deployment')
                self.assertEqual(pdb['spec']['selector'], deployment['spec']['selector'])
                self.assertEqual(pdb['metadata']['namespace'], 'custom')
                self.assertEqual(pdb['metadata']['name'], deployment['metadata']['name'])
                self.assertEqual(deployment['spec']['replicas'], 3)
                self.assertEqual({k: pdb['spec'][k] for k in ('minAvailable', 'maxUnavailable') if k in pdb['spec']}, expected)
        render({'podDisruptionBudget': {'enabled': True, 'minAvailable': 1, 'maxUnavailable': 0}}, expect_error='mutually exclusive')
        docs = render({'podDisruptionBudget': {'enabled': True, 'unhealthyPodEvictionPolicy': 'AlwaysAllow'}})
        self.assertEqual(resource(docs, 'PodDisruptionBudget')['spec']['unhealthyPodEvictionPolicy'], 'AlwaysAllow')

    def test_unsupported_configuration(self):
        for values, message in [({'extraArgs': ['--foo']}, 'extraArgs is unsupported'), ({'featureGates': 'Example=true'}, 'featureGates is unsupported'), ({'maxConcurrentChallenges': 60}, 'maxConcurrentChallenges is unsupported')]:
            with self.subTest(values=values):
                render(values, expect_error=message)

    def test_network_policy(self):
        for production in (False, True):
            with self.subTest(production=production):
                docs = render({'networkPolicy': {'enabled': True}}, production=production)
                policy = resource(docs, 'NetworkPolicy')
                self.assertEqual(policy['spec']['podSelector'], resource(docs, 'Deployment')['spec']['selector'])
                self.assertEqual(policy['spec']['policyTypes'], ['Ingress', 'Egress'])
                source, = policy['spec']['ingress'][0]['from']
                self.assertEqual(source['podSelector']['matchLabels'], {'app.kubernetes.io/name': 'prometheus'})
                if production:
                    self.assertEqual(source['namespaceSelector']['matchLabels'], {'kubernetes.io/metadata.name': 'monitoring'})
                else:
                    self.assertNotIn('namespaceSelector', source)
                self.assertEqual(policy['spec']['ingress'][0]['ports'], [{'port': 'http', 'protocol': 'TCP'}])
                self.assertEqual({(port['port'], port['protocol']) for rule in policy['spec']['egress'] for port in rule['ports']}, {(443, 'TCP'), (6443, 'TCP'), (53, 'TCP'), (53, 'UDP')})
        docs = render({'networkPolicy': {'enabled': True, 'ingress': [], 'egress': []}})
        policy = resource(docs, 'NetworkPolicy')
        self.assertEqual(policy['spec']['ingress'], [])
        self.assertEqual(policy['spec']['egress'], [])

    def test_readme_manifests(self):
        # Parse every YAML example to catch malformed manifests in either language.
        for filename in ('README.md', 'README-es.md'):
            text = (ROOT / filename).read_text()
            for block in text.split('```yaml\n')[1:]:
                docs = list(yaml.safe_load_all(block.split('```')[0]))
                for obj in docs:
                    self.assertIn(obj['apiVersion'], ('v1', 'apps/v1', 'rbac.authorization.k8s.io/v1', 'monitoring.coreos.com/v1'))
                    if obj['kind'] == 'ClusterRole':
                        self.assertEqual(set(obj['rules'][0]['resources']), {'resourceclaims', 'resourceslices', 'deviceclasses'})


if __name__ == '__main__':
    unittest.main()
