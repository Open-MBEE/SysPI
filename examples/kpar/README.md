# Vehicle KPAR example

Vehicle uses Drive, and Drive uses Power. Each archive contains one model and
the URLs of its direct dependencies. The source files and the Demo consumer are
in [models](models/).

| Builder | Power | Drive | Vehicle |
|---|---|---|---|
| SysPI | [Power](syspi/power-1.0.0.kpar) | [Drive](syspi/drive-1.0.0.kpar) | [Vehicle](syspi/vehicle-1.0.0.kpar) |
| Sysand | [Power](sysand/power-1.0.0.kpar) | [Drive](sysand/drive-1.0.0.kpar) | [Vehicle](sysand/vehicle-1.0.0.kpar) |

## Try the archives

From the repository root, start a server in its own terminal:

```sh
python -m http.server 8765 --bind 127.0.0.1 --directory examples/kpar
```

Keep that port and directory layout. Drive and Vehicle contain dependency URLs
that point back to this server. For remote sharing, rebuild with URLs on your
own host.

In a fresh project directory, copy `models/Demo.sysml` to `Demo.sysml`.
Use either `syspi` or `sysand` in the URL to choose the archive set.

With SysPI:

```sh
syspi init -publisher acme -name demo -version 1.0.0
syspi add http://127.0.0.1:8765/sysand/vehicle-1.0.0.kpar
syspi install
```

Vehicle, Drive, and Power appear as KPARs under `lib/` and as entries in
`.project.lock.json`.

With the published Sysand 0.2.1 CLI:

```sh
sysand init --publisher acme --name demo --version 1.0.0
sysand include Demo.sysml
sysand add --no-index http://127.0.0.1:8765/syspi/vehicle-1.0.0.kpar
sysand sync --no-index
sysand sources
```

The three dependencies appear in `sysand-lock.toml`. The source list includes
Demo's source and the three installed model files.

## Verification

`go test ./tests/e2e -run '^TestExampleKPARs$'` checks the committed archives,
their dependency URLs, and the archived model sources against the example files.

The six archives were built with SysPI at `dca1a6a` on `develop` and the published Sysand
0.2.1 CLI. Both CLIs installed the complete dependency chain from both archive
sets in fresh projects. All six ZIP CRCs, recorded SHA-256 checksums, metamodel
declarations, and direct dependency URLs were checked. This tested packaging
and dependency resolution. Loading the models in a modeling tool is a separate
check.
