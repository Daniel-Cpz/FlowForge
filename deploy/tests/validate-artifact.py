import pathlib, re, sys
path, expected_sha = sys.argv[1:]
lines = pathlib.Path(path).read_text().splitlines()
values = {}
for line in lines:
    key, value = line.split('=', 1)
    if key in values:
        raise SystemExit('duplicate release key')
    values[key] = value
assert set(values) == {'FLOWFORGE_RELEASE_SHA', 'FLOWFORGE_BACKEND_IMAGE', 'FLOWFORGE_GATEWAY_IMAGE'}
assert re.fullmatch('[0-9a-f]{40}', expected_sha)
assert values['FLOWFORGE_RELEASE_SHA'] == expected_sha
for component in ('BACKEND', 'GATEWAY'):
    assert re.fullmatch(r'ghcr\.io/daniel-cpz/flowforge-' + component.lower() + r'@sha256:[0-9a-f]{64}', values['FLOWFORGE_' + component + '_IMAGE'])
print('PASS release revision/digest artifact')
