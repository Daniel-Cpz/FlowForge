"""Real disposable production stack; stdlib only, Linux CI or Windows Docker.

No retained database is mounted. Every cleanup checks generated project labels.
"""
import base64
import contextlib
import hashlib
import json
import os
import pathlib
import re
import secrets
import shutil
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.error
import urllib.request

ROOT = pathlib.Path(__file__).resolve().parents[2]
SHA = os.environ.get('FLOWFORGE_RELEASE_SHA') or subprocess.check_output(
    ['git', '-c', 'safe.directory=' + ROOT.as_posix(), 'rev-parse', 'HEAD'], cwd=ROOT, text=True).strip()
assert re.fullmatch('[0-9a-f]{40}', SHA)
PROJECT = 'ffp10-' + time.strftime('%Y%m%d%H%M%S', time.gmtime()) + '-' + secrets.token_hex(4)
assert re.fullmatch(r'ffp10-[0-9]{14}-[0-9a-f]{8}', PROJECT)
(ROOT / 'tmp').mkdir(exist_ok=True)
TEMP = pathlib.Path(tempfile.mkdtemp(prefix=PROJECT + '-', dir=ROOT / 'tmp'))
ENV = TEMP / 'env'
OVERLAY = TEMP / 'test.yml'
BACKEND = 'flowforge-phase10-backend:' + SHA
GATEWAY = 'flowforge-phase10-gateway:' + SHA
TOOLS = 'flowforge-phase10-tools:' + SHA
PASSWORD = secrets.token_hex(32)
AUTH_PASSWORD = secrets.token_hex(24)


def run(args, *, data=None, capture=True, check=True):
    p = subprocess.run(args, input=data, stdout=subprocess.PIPE if capture else None,
                       stderr=subprocess.PIPE if capture else None, cwd=ROOT, text=True)
    if check and p.returncode:
        # Compose config / auth / environment outputs can contain credentials.
        raise RuntimeError('command failed: ' + args[0] + ' ' + args[1] + ' (exit ' + str(p.returncode) + ')')
    return p.stdout or ''


def dc(*args, **kw):
    return run(['docker', 'compose', '--project-directory', str(ROOT / 'deploy'), '--env-file', str(ENV),
                '-p', PROJECT, '-f', str(ROOT / 'deploy/compose.prod.yml'), '-f', str(OVERLAY), *args], **kw)


def wait(fn, seconds=90, label='condition'):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        try:
            if fn():
                return
        except (urllib.error.URLError, OSError, ValueError, RuntimeError):
            pass
        time.sleep(0.2)
    raise RuntimeError('timeout: ' + label)


def http(base, path, body=None, auth=False, tls=None, method=None):
    headers = {'Content-Type': 'application/json'}
    if auth:
        headers['Authorization'] = 'Basic ' + base64.b64encode(('demo:' + AUTH_PASSWORD).encode()).decode()
    request = urllib.request.Request(base + path, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
    try:
        response = urllib.request.urlopen(request, timeout=5, context=tls)
    except urllib.error.HTTPError as e:
        response = e
    with response:
        return response.status, response.read()


def json_request(base, path, body=None, method=None):
    status, data = http(base, path, body, method=method)
    assert status in (200, 201), 'API status ' + str(status)
    return json.loads(data)


def connect_ws(port, origin, tls=None, auth=False):
    connection = socket.create_connection(('127.0.0.1', port), timeout=5)
    host = '127.0.0.1'
    if tls is not None:
        connection = tls.wrap_socket(connection, server_hostname='localhost')
        host = 'localhost'
    nonce = base64.b64encode(secrets.token_bytes(16)).decode()
    request = ('GET /api/v1/ws HTTP/1.1\r\nHost: ' + host + ':' + str(port)
               + '\r\nOrigin: ' + origin + '\r\nConnection: Upgrade\r\nUpgrade: websocket\r\nSec-WebSocket-Version: 13\r\nSec-WebSocket-Key: ' + nonce + '\r\n\r\n')
    if auth:
        credential = base64.b64encode(('demo:' + AUTH_PASSWORD).encode()).decode()
        request = request[:-2] + 'Authorization: Basic ' + credential + '\r\n\r\n'
    connection.sendall(request.encode())
    response = b''
    while b'\r\n\r\n' not in response:
        response += connection.recv(1)  # do not consume the initial frame
        assert len(response) < 16384
    assert b' 101 ' in response.split(b'\r\n')[0]
    expected = base64.b64encode(hashlib.sha1((nonce + '258EAFA5-E914-47DA-95CA-C5AB0DC85B11').encode()).digest())
    assert expected.lower() in response.lower()
    return connection


def read_hint(connection):
    def exact(size):
        data = b''
        while len(data) < size:
            part = connection.recv(size - len(data))
            assert part, 'WS closed before hint'
            data += part
        return data
    header = exact(2)
    assert header[0] & 15 == 1 and not header[1] & 128
    size = header[1] & 127
    if size == 126:
        size = int.from_bytes(exact(2), 'big')
    assert size <= 2048
    return json.loads(exact(size))


def terminal(base, job_id, status='SUCCEEDED'):
    wait(lambda: json_request(base, '/api/v1/jobs/' + job_id)['status'] == status, 80, 'job terminal ' + status)
    return json_request(base, '/api/v1/jobs/' + job_id)


def sql(query, db='flowforge'):
    return dc('exec', '-T', 'postgres', 'psql', '-U', 'flowforge', '-d', db, '-Atc', query).strip()


try:
    if os.environ.get('FLOWFORGE_SKIP_BUILD') != 'true':
        run(['docker', 'build', '--build-arg', 'REVISION=' + SHA, '-t', BACKEND, '.'], capture=False)
        run(['docker', 'build', '--build-arg', 'REVISION=' + SHA, '-t', GATEWAY, '-f', 'deploy/gateway/Dockerfile', '.'], capture=False)
        run(['docker', 'build', '--target', 'development', '-t', TOOLS, '.'], capture=False)
    for image in (BACKEND, GATEWAY):
        assert run(['docker', 'image', 'inspect', '--format', '{{index .Config.Labels "org.opencontainers.image.revision"}}', image]).strip() == SHA
    bcrypt = run(['docker', 'run', '--rm', GATEWAY, 'caddy', 'hash-password', '--plaintext', AUTH_PASSWORD]).strip()
    ENV.write_text('\n'.join([
        'FLOWFORGE_HOME=' + TEMP.as_posix(), 'FLOWFORGE_MODE=private', 'FLOWFORGE_BACKEND_IMAGE=' + BACKEND,
        'FLOWFORGE_GATEWAY_IMAGE=' + GATEWAY, 'FLOWFORGE_POSTGRES_PASSWORD=' + PASSWORD,
        'FLOWFORGE_REDIS_PASSWORD=' + PASSWORD, 'FLOWFORGE_WS_ORIGINS=http://127.0.0.1:8180',
    ]) + '\n')
    ENV.chmod(0o600)
    OVERLAY.write_text('''services:
  postgres:
    volumes: !override [pg_data:/var/lib/postgresql, pg_tls:/tls:ro]
  gateway:
    ports: ["127.0.0.1::8080"]
  public-gateway:
    image: ''' + GATEWAY + '''
    networks: [private]
    environment:
      FLOWFORGE_PUBLIC_HOST: localhost
      FLOWFORGE_BASIC_USER: demo
      FLOWFORGE_BASIC_HASH: "''' + bcrypt.replace('$', '$$') + '''"
    volumes:
      - ''' + (ROOT / 'deploy/gateway/Caddyfile.public').as_posix() + ''':/etc/caddy/Caddyfile:ro
      - test_caddy_data:/data
    ports: ["127.0.0.1::443"]
volumes:
  pg_tls: {}
  test_caddy_data: {}
''')
    # Resolve actual config and assert no accidental exposure before starting.
    config = json.loads(dc('config', '--format', 'json'))
    for name, service in config['services'].items():
        for port in service.get('ports', []):
            assert name in ('gateway', 'public-gateway') and port['host_ip'] == '127.0.0.1'
        assert not service.get('build')
    tls_volume = PROJECT + '_pg_tls'
    run(['docker', 'volume', 'create', '--label', 'com.docker.compose.project=' + PROJECT, tls_volume])
    run(['docker', 'run', '--rm', '-v', tls_volume + ':/tls', TOOLS, 'sh', '-c',
         'openssl req -x509 -newkey rsa:3072 -nodes -days 1 -subj /CN=postgres -keyout /tls/server.key -out /tls/server.crt 2>/dev/null && chown 70:70 /tls/* && chmod 600 /tls/server.key && chmod 644 /tls/server.crt'])
    dc('up', '-d', 'postgres', 'redis')
    wait(lambda: sql('SELECT 1') == '1', label='postgres')
    dc('run', '--rm', 'migrate')
    assert sql('SELECT max(version) FROM schema_migrations') == '8'
    dc('up', '-d', '--scale', 'worker=2', 'api', 'worker', 'gateway', 'public-gateway')
    port = int(dc('port', 'gateway', '8080').strip().rsplit(':', 1)[1])
    base = 'http://127.0.0.1:' + str(port)
    wait(lambda: http(base, '/ready')[0] == 200, label='API ready through gateway')
    wait(lambda: len([w for w in json_request(base, '/api/v1/workers')['workers'] if w['status'] != 'OFFLINE']) == 2, label='2 workers')
    # pg_stat_ssl is checked for real application connections, not only server ssl=on.
    assert int(sql("SELECT count(*) FROM pg_stat_ssl s JOIN pg_stat_activity a USING(pid) WHERE a.usename='flowforge' AND s.ssl")) >= 2
    assert dc('exec', '-T', 'postgres', 'stat', '-c', '%a', '/tls/server.key').strip() == '600'
    assert 'NOAUTH' in dc('exec', '-T', 'redis', 'redis-cli', 'ping')
    assert http(base, '/metrics')[0] == 404
    assert '<div id="root">' in http(base, '/')[1].decode()
    assert http(base, '/jobs/example')[0] == 200  # SPA fallback
    for service, endpoint in [('api', 'http://127.0.0.1:8080/metrics'), ('worker', 'http://127.0.0.1:9091/metrics')]:
        assert 'flowforge_' in dc('exec', '-T', service, 'wget', '-qO-', endpoint)
    print('PASS production schema 8 / PG TLS / Redis auth / private listeners / static SPA / internal metrics', flush=True)
    # Trust Caddy's disposable local CA; never skip certificate validation.
    ca = TEMP / 'test-ca.crt'
    pub_id = dc('ps', '-q', 'public-gateway').strip()
    wait(lambda: 'BEGIN CERTIFICATE' in run(['docker', 'exec', pub_id, 'cat', '/data/caddy/pki/authorities/local/root.crt']), label='local test CA')
    run(['docker', 'cp', pub_id + ':/data/caddy/pki/authorities/local/root.crt', str(ca)])
    context = ssl.create_default_context(cafile=str(ca))
    pub_port = dc('port', 'public-gateway', '443').strip().rsplit(':', 1)[1]
    public = 'https://localhost:' + pub_port
    wait(lambda: http(public, '/api/v1/jobs', tls=context)[0] == 401, label='HTTPS auth denial')
    assert http(public, '/api/v1/jobs', auth=True, tls=context)[0] == 200
    assert http(public, '/metrics', auth=True, tls=context)[0] == 404
    assert http(public, '/api/v1/ws', tls=context)[0] == 401
    with contextlib.closing(connect_ws(int(pub_port), 'https://localhost:' + pub_port, tls=context, auth=True)) as ws:
        assert read_hint(ws)['event'] == 'system.changed'
    print('PASS HTTPS gateway + unauthenticated denial + authenticated REST (disposable trusted local CA)', flush=True)
    with contextlib.closing(connect_ws(port, 'http://127.0.0.1:8180')) as ws:
        job = json_request(base, '/api/v1/jobs', {'type': 'SLEEP', 'payload': {'duration_ms': 100}})
        deadline = time.monotonic() + 5
        while True:
            hint = read_hint(ws)
            if hint['event'] == 'job.changed' and hint['resource_id'] == job['id']:
                break
            assert time.monotonic() < deadline, 'job invalidation hint timeout'
        terminal(base, job['id'])
    with contextlib.closing(connect_ws(port, 'http://127.0.0.1:8180')):
        assert json_request(base, '/api/v1/dashboard/summary')
    print('PASS WebSocket proxy handshake / reconnect + REST repair / SLEEP completion', flush=True)
    schedule = json_request(base, '/api/v1/schedules', {'type': 'SLEEP', 'payload': {'duration_ms': 25}, 'interval_seconds': 60, 'required_capabilities': ['sleep']})
    json_request(base, '/api/v1/schedules/' + schedule['id'] + '/cancel', method='POST')
    dlq = json_request(base, '/api/v1/jobs', {'type': 'UNKNOWN', 'payload': {}, 'max_attempts': 1})
    terminal(base, dlq['id'], 'DEAD_LETTER')
    json_request(base, '/api/v1/jobs/' + dlq['id'] + '/retry', method='POST')
    terminal(base, dlq['id'], 'DEAD_LETTER')
    assert len(json_request(base, '/api/v1/jobs/' + dlq['id'] + '/attempts')['attempts']) == 2
    print('PASS schedule/capability + DLQ inspect/redrive', flush=True)
    long = json_request(base, '/api/v1/jobs', {'type': 'SLEEP', 'payload': {'duration_ms': 10000}, 'max_attempts': 3})
    wait(lambda: json_request(base, '/api/v1/jobs/' + long['id'])['status'] == 'RUNNING', label='worker owner')
    owner = json_request(base, '/api/v1/jobs/' + long['id'])['assigned_worker']
    killed = False
    for worker_id in dc('ps', '-q', 'worker').split():
        assert run(['docker', 'inspect', '--format', '{{index .Config.Labels "com.docker.compose.project"}}', worker_id]).strip() == PROJECT
        if owner in run(['docker', 'logs', worker_id]):
            run(['docker', 'kill', worker_id]); killed = True; break
    assert killed, 'could not identify the executing worker'
    terminal(base, long['id'])
    attempts = json_request(base, '/api/v1/jobs/' + long['id'] + '/attempts')['attempts']
    assert len(attempts) >= 2 and attempts[0]['error'] == 'lease_expired'
    print('PASS hard worker kill / lease recovery / new Attempt / final success', flush=True)
    # Stop app writers for deterministic identity comparison; never touches real DB.
    dc('stop', 'gateway', 'public-gateway', 'api', 'worker')
    query = "SELECT json_build_object('schema',(SELECT max(version) FROM schema_migrations),'jobs',(SELECT json_agg(id ORDER BY id) FROM jobs),'attempts',(SELECT json_agg(id ORDER BY id) FROM job_attempts),'schedules',(SELECT json_agg(id ORDER BY id) FROM job_schedules))"
    before = sql(query)
    archive = TEMP / 'restore.dump'
    # Binary pg_dump capture avoids Windows newline/text conversion.
    dump_args = ['docker', 'compose', '--project-directory', str(ROOT / 'deploy'), '--env-file', str(ENV), '-p', PROJECT, '-f', str(ROOT / 'deploy/compose.prod.yml'), '-f', str(OVERLAY), 'exec', '-T', 'postgres', 'pg_dump', '-U', 'flowforge', '-d', 'flowforge', '-Fc']
    with archive.open('wb') as output:
        subprocess.run(dump_args, stdout=output, check=True, cwd=ROOT)
    dc('exec', '-T', 'postgres', 'createdb', '-U', 'flowforge', 'flowforge_restore_test')
    restore_args = dump_args[:-6] + ['pg_restore', '-U', 'flowforge', '-d', 'flowforge_restore_test', '--exit-on-error', '--no-owner']
    with archive.open('rb') as source:
        subprocess.run(restore_args, stdin=source, check=True, cwd=ROOT)
    assert sql(query, 'flowforge_restore_test') == before
    dc('exec', '-T', 'postgres', 'dropdb', '-U', 'flowforge', 'flowforge_restore_test')
    print('PASS pg_dump custom archive / disposable restore schema + Job/Attempt/Schedule IDs', flush=True)
    dc('restart', 'postgres', 'redis')
    wait(lambda: sql('SELECT 1') == '1', label='persistent PostgreSQL restart')
    dc('up', '-d', '--scale', 'worker=2', 'api', 'worker', 'gateway')
    # Docker may reallocate an ephemeral published port when starting a stopped
    # gateway; production uses a fixed loopback port instead.
    port = int(dc('port', 'gateway', '8080').strip().rsplit(':', 1)[1])
    base = 'http://127.0.0.1:' + str(port)
    wait(lambda: http(base, '/ready')[0] == 200, label='restart readiness')
    assert json_request(base, '/api/v1/jobs/' + job['id'])['status'] == 'SUCCEEDED'
    print('PASS named-volume restart persistence / previous Job readable', flush=True)
finally:
    ids = run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + PROJECT]).split()
    for cid in ids:
        assert run(['docker', 'inspect', '--format', '{{index .Config.Labels "com.docker.compose.project"}}', cid]).strip() == PROJECT
    if ENV.exists() and OVERLAY.exists():
        dc('down', '--volumes', '--remove-orphans')
    # Compose can fail before first up; remove only the explicitly labelled TLS volume.
    volumes = run(['docker', 'volume', 'ls', '-q', '--filter', 'label=com.docker.compose.project=' + PROJECT]).split()
    for volume in volumes:
        assert run(['docker', 'volume', 'inspect', '--format', '{{index .Labels "com.docker.compose.project"}}', volume]).strip() == PROJECT
        run(['docker', 'volume', 'rm', volume])
    assert not run(['docker', 'ps', '-aq', '--filter', 'label=com.docker.compose.project=' + PROJECT]).strip()
    assert TEMP.parent == ROOT / 'tmp' and TEMP.name.startswith(PROJECT + '-')
    shutil.rmtree(TEMP)
    print('PASS generated production stack cleanup ' + PROJECT, flush=True)
