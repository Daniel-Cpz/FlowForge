"""Structural workflow/Compose checks complement real production-stack smoke."""
import pathlib
import yaml  # Test-only dependency: PyYAML==6.0.2

root = pathlib.Path(__file__).resolve().parents[2]
for name in ('release-images', 'deploy'):
    data = yaml.safe_load((root / '.github/workflows' / (name + '.yml')).read_text())
    triggers = data.get('on', data.get(True))  # YAML 1.1 treats on as boolean
    assert set(triggers) == {'workflow_dispatch'}
    assert data['concurrency']['cancel-in-progress'] is False
deploy = yaml.safe_load((root / '.github/workflows/deploy.yml').read_text())
assert deploy['jobs']['deploy']['environment'] == 'flowforge-cloud'
remote = (root / 'deploy/scripts/remote-deploy.sh').read_text()
assert 'StrictHostKeyChecking=yes' in remote and 'StrictHostKeyChecking=no' not in remote
script = '\n'.join(line for line in (root / 'deploy/scripts/deploy.sh').read_text().splitlines() if not line.lstrip().startswith('#'))
for forbidden in ('migrate down', 'reset --hard', 'push --force'):
    assert forbidden not in script
base = yaml.safe_load((root / 'deploy/compose.prod.yml').read_text())
for name, service in base['services'].items():
    assert not service.get('ports') and not service.get('build')
    assert service.get('restart') in ('unless-stopped', 'no')
    assert service.get('init') is True
for mode in ('private', 'public'):
    overlay = yaml.safe_load((root / 'deploy' / ('compose.' + mode + '.yml')).read_text())
    for name, service in overlay['services'].items():
        for port in service.get('ports', []):
            assert name == 'gateway' and mode == 'public' or port.startswith('127.0.0.1:')
print('PASS dispatch/environment/concurrency/pinned-SSH + production port isolation')
