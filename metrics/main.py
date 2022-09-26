from pathlib import Path
import os
import urllib.request
from bottle import route, run


UPSTREAM_URL = os.environ.get('UPSTREAM_URL', 'http://127.0.0.1:8080/metrics')
PORT = int(os.environ.get('METRICS_PORT', "8082"))
METRICS_DIR = os.environ.get('METRICS_DIR', "/extra_metrics")


@route('/metrics')
def metrics():
    with urllib.request.urlopen(UPSTREAM_URL) as req:
        contents = req.read().decode('utf-8')

    for file in Path(METRICS_DIR).glob('*.prom'):
        if not contents.endswith('\n'):
            contents += '\n'
        contents += file.read_text()

    return contents


if __name__ == "__main__":
    run(host='0.0.0.0', port=PORT)