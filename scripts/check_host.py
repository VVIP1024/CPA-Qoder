#!/usr/bin/env python3
"""Exercise native unload against CPA's original queue using a temporary Go overlay."""
import json
import os
from pathlib import Path
import subprocess
import tempfile

base = Path(__file__).resolve().parents[1]
host = Path(os.environ.get("CPA_DIR", str(base.parent / "CLIProxyAPI"))).resolve()
library = next(p for p in (base / "build/plugins").glob("cpa-qoder.*")
               if p.suffix in (".dylib", ".so"))
with tempfile.TemporaryDirectory(prefix="cpa-qoder-host-check-") as directory:
    overlay = Path(directory) / "overlay.json"
    virtual = host / "internal/pluginhost/native_external_compat_test.go"
    overlay.write_text(json.dumps({"Replace": {str(virtual): str(base / "scripts/host_compat_test.go.txt")}}))
    env = dict(os.environ, CPA_TEST_LIBRARY=str(library))
    subprocess.run(["go", "test", "-overlay", str(overlay), "./internal/pluginhost",
                    "-run", "^TestNativePluginClosedHostBackpressure$", "-count=1", "-timeout=30s", "-v"],
                   cwd=host, env=env, check=True)
