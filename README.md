# ORP NGINX configuration importer

Standalone Go API and Vue UI for retaining `nginx -T` output, previewing its conversion, and importing supported HTTP resources into ORP through the existing control-plane API.

## Run locally

```sh
cd orp-nginx-importer/web && npm install
cd .. && ./dev.sh
```

Open http://localhost:8091. Vite proxies `/api` to Go on `:8090`; the Go server can also serve the production UI from `web/dist`. `NGINX_IMPORTER_ADDR` sets the Go listen address (default `:8090`). `NGINX_IMPORTER_STORAGE` selects the durable source/batch directory (default `./data`); `ORP_BACKEND_URL` points to ORP (default `http://127.0.0.1:8081`). Do not place the storage directory in a public web root.

## Workflow

1. Generate a dump with `nginx -T > nginx-T.txt` and upload it (32 MB maximum).
2. The original bytes are stored under `data/sources/` and SHA-256 plus parsed preview are stored under `data/batches/`.
3. Inspect resources and unsupported directives, then provide an ORP center UUID and authorized Bearer token to import supported HTTP upstreams, targets, servers, and simple locations.
4. Review unsupported entries and imported resources in ORP before creating a release. Import does not publish, validate on nodes, reload, or restart.

## Current conversion boundary

HTTP upstream `server` targets, weight/backup, HTTP `server_name`, listen port, simple `location`, `proxy_pass` references to named upstreams, `root`, `alias`, and `return` are mapped. Unknown directives are retained in the unsupported report with source file, line, and context. TLS listeners are reported for manual certificate mapping and imported without enabling TLS. Variables, complex location matching, upstream policies, includes that are absent from the dump, arbitrary modules, stream/mail blocks, and directive semantics beyond the listed mappings need manual work. The parser never executes NGINX commands or follows paths from the uploaded dump.

The batch write uses ORP CRUD endpoints. A multi-request import is not transactional: if ORP rejects a later resource, earlier resources may already exist. Inspect the batch and ORP before retrying.
