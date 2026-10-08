package importer

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type API struct {
	dir, orpURL string
	mu          sync.Mutex
	batches     map[string]Batch
}

func New(dir, orpURL string) (*API, error) {
	if err := os.MkdirAll(filepath.Join(dir, "sources"), 0700); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Join(dir, "batches"), 0700); err != nil {
		return nil, err
	}
	a := &API{dir: dir, orpURL: strings.TrimRight(orpURL, "/"), batches: map[string]Batch{}}
	entries, _ := os.ReadDir(filepath.Join(dir, "batches"))
	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, "batches", e.Name()))
		if err == nil {
			var v Batch
			if json.Unmarshal(b, &v) == nil {
				a.batches[v.ID] = v
			}
		}
	}
	return a, nil
}
func (a *API) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /api/batches", a.list)
	m.HandleFunc("POST /api/batches", a.upload)
	m.HandleFunc("GET /api/batches/{id}", a.get)
	m.HandleFunc("POST /api/batches/{id}/import", a.importBatch)
	m.Handle("/", http.FileServer(http.Dir("web/dist")))
	return cors(m)
}
func cors(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Authorization, Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		if r.Method == "OPTIONS" {
			w.WriteHeader(204)
			return
		}
		next.ServeHTTP(w, r)
	})
}
func (a *API) upload(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 32<<20)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		http.Error(w, "nginx -T dump must be at most 32 MB: "+err.Error(), 413)
		return
	}
	f, h, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "multipart field 'file' is required", 400)
		return
	}
	defer f.Close()
	src, err := io.ReadAll(f)
	if err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(bytes.TrimSpace(src)) == 0 {
		http.Error(w, "file is empty", 400)
		return
	}
	if !strings.Contains(string(src), "nginx version") && !strings.Contains(string(src), "configuration file ") {
		http.Error(w, "expected nginx -T output with configuration file markers", 400)
		return
	}
	sum := sha256.Sum256(src)
	id := uuid.NewString()
	name := filepath.Base(h.Filename)
	if name == "." || name == "/" || name == "" {
		name = "nginx-T.txt"
	}
	if err = os.WriteFile(filepath.Join(a.dir, "sources", id+".txt"), src, 0600); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	root, issues := parseDump(string(src))
	batch := Batch{ID: id, Name: name, SHA256: hex.EncodeToString(sum[:]), Bytes: int64(len(src)), UploadedAt: time.Now().UTC(), SourceFile: id + ".txt"}
	preview := convert(root, issues)
	batch.Preview = &preview
	a.mu.Lock()
	a.batches[id] = batch
	err = a.persist(batch)
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	write(w, 201, batch)
}
func (a *API) list(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Batch, 0, len(a.batches))
	for _, b := range a.batches {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UploadedAt.After(out[j].UploadedAt) })
	write(w, 200, out)
}
func (a *API) get(w http.ResponseWriter, r *http.Request) {
	a.mu.Lock()
	b, ok := a.batches[r.PathValue("id")]
	a.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	write(w, 200, b)
}
func (a *API) persist(b Batch) error {
	raw, e := json.MarshalIndent(b, "", "  ")
	if e != nil {
		return e
	}
	return os.WriteFile(filepath.Join(a.dir, "batches", b.ID+".json"), raw, 0600)
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

type orpClient struct {
	base, token string
	client      *http.Client
}

func (c *orpClient) request(method, path string, body any, out any) error {
	var data io.Reader
	if body != nil {
		b, e := json.Marshal(body)
		if e != nil {
			return e
		}
		data = bytes.NewReader(b)
	}
	req, e := http.NewRequest(method, c.base+path, data)
	if e != nil {
		return e
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	res, e := c.client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 4096))
		return fmt.Errorf("ORP API %s %s returned %d: %s", method, path, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	if out != nil {
		return json.NewDecoder(res.Body).Decode(out)
	}
	return nil
}
func (a *API) importBatch(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var input struct {
		CenterID string `json:"centerId"`
		Token    string `json:"token"`
	}
	if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil || input.Token == "" {
		http.Error(w, "centerId and ORP bearer token are required", 400)
		return
	}
	if _, e := uuid.Parse(input.CenterID); e != nil {
		http.Error(w, "centerId must be a UUID", 400)
		return
	}
	a.mu.Lock()
	b, ok := a.batches[id]
	a.mu.Unlock()
	if !ok || b.Preview == nil {
		http.NotFound(w, r)
		return
	}
	if b.ImportedAt != nil {
		http.Error(w, "batch was already imported; uploads are immutable", 409)
		return
	}
	if b.ImportCenter != "" {
		http.Error(w, "a previous import attempt partially created ORP resources; inspect ORP before retrying", 409)
		return
	}
	base := a.orpURL
	if base == "" {
		base = os.Getenv("ORP_BACKEND_URL")
	}
	if base == "" {
		http.Error(w, "ORP_BACKEND_URL is not configured", 503)
		return
	}
	c := &orpClient{base: strings.TrimRight(base, "/"), token: input.Token, client: &http.Client{Timeout: 20 * time.Second}}
	path := "/api/centers/" + url.PathEscape(input.CenterID)
	b.ImportCenter = input.CenterID
	a.mu.Lock()
	a.batches[id] = b
	err := a.persist(b)
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	upstreamIDs := map[string]string{}
	upstreamNames := map[string]bool{}
	var existing []struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	if e := c.request("GET", path+"/http/upstreams", nil, &existing); e != nil {
		http.Error(w, e.Error(), 502)
		return
	}
	for _, u := range existing {
		upstreamNames[u.Name] = true
		upstreamIDs[u.Name] = u.ID
	}
	for _, u := range b.Preview.Upstreams {
		if upstreamNames[u.Name] {
			continue
		}
		created := struct {
			ID string `json:"id"`
		}{}
		body := map[string]any{"name": u.Name, "keepaliveConnections": 1, "zoneSizeKilobytes": 8, "healthCheckEnabled": false, "healthCheckType": "HTTP", "healthCheckPath": "/", "healthCheckIntervalSeconds": 5, "healthCheckTimeoutMilliseconds": 1000, "healthCheckExpectedStatus": 200, "healthCheckRequestHeaders": []string{}, "healthCheckRise": 1, "healthCheckFall": 1}
		if e := c.request("POST", path+"/http/upstreams", body, &created); e != nil {
			http.Error(w, "create upstream "+u.Name+": "+e.Error(), 502)
			return
		}
		upstreamIDs[u.Name] = created.ID
		for _, t := range u.Targets {
			target := map[string]any{"targetHost": t.Host, "targetPort": t.Port, "weight": t.Weight, "maxFails": 1, "failTimeoutSeconds": 10, "resolveEnabled": false, "backup": t.Backup, "enabled": true}
			if e := c.request("POST", path+"/http/upstreams/"+created.ID+"/targets", target, nil); e != nil {
				http.Error(w, "create upstream target "+u.Name+": "+e.Error(), 502)
				return
			}
		}
	}
	count := 0
	for _, s := range b.Preview.Servers {
		for _, domain := range s.Names {
			upID := ""
			if len(s.Locations) == 0 && len(b.Preview.Upstreams) == 1 {
				upID = upstreamIDs[b.Preview.Upstreams[0].Name]
			}
			body := map[string]any{"domain": domain, "listenPort": s.ListenPort, "sslEnabled": false, "upstreamId": nil, "accessLog": "", "errorLog": ""}
			if upID != "" {
				body["upstreamId"] = upID
			}
			created := struct {
				ID string `json:"id"`
			}{}
			if e := c.request("POST", path+"/http/servers", body, &created); e != nil {
				http.Error(w, "create server "+domain+": "+e.Error(), 502)
				return
			}
			count++
			for _, loc := range s.Locations {
				locationBody := map[string]any{"path": loc.Path, "methods": []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS", "HEAD"}, "contentTypes": []string{}, "headerLengthMin": 0, "headerLengthMax": 0, "bodyLengthMin": 0, "bodyLengthMax": 0, "upstreamId": nil, "proxyConnectTimeoutMs": 60000, "proxyReadTimeoutMs": 60000, "proxySendTimeoutMs": 60000, "rateLimitEnabled": false, "ratePerSecond": 1, "rateLimitBurst": 0, "rateLimitNodelay": false, "dynamicDnsEnabled": false}
				if loc.ProxyPass != "" && proxyUpstream(loc.ProxyPass) != "" {
					if mapped := upstreamIDs[proxyUpstream(loc.ProxyPass)]; mapped != "" {
						locationBody["upstreamId"] = mapped
					} else {
						http.Error(w, "proxy_pass upstream is not present in imported HTTP upstreams: "+loc.ProxyPass, 422)
						return
					}
				}
				if loc.ProxyPass != "" && proxyUpstream(loc.ProxyPass) == "" {
					http.Error(w, "unsupported proxy_pass target cannot be imported as a fixed ORP upstream: "+loc.ProxyPass, 422)
					return
				}
				var locCreated map[string]any
				if e := c.request("POST", path+"/http/servers/"+created.ID+"/locations", locationBody, &locCreated); e != nil {
					http.Error(w, "create location "+loc.Path+": "+e.Error(), 502)
					return
				}
				if loc.Root != "" || loc.Alias != "" || loc.Return != "" {
					fields := map[string]any{"action": "PROXY", "rootPath": nil, "aliasPath": nil, "returnStatus": nil, "returnBody": nil, "returnContentTypeMode": "CUSTOM", "returnContentType": nil, "responseHeaders": []string{}}
					if loc.Root != "" {
						fields["action"] = "STATIC"
						fields["rootPath"] = loc.Root
					}
					if loc.Alias != "" {
						fields["action"] = "STATIC"
						fields["aliasPath"] = loc.Alias
					}
					if loc.Return != "" {
						fields["action"] = "RETURN"
						parts := strings.SplitN(loc.Return, " ", 2)
						status := 200
						if len(parts) > 1 {
							status = number(parts[0], 200)
						}
						fields["returnStatus"] = status
						if len(parts) > 1 {
							fields["returnBody"] = strings.Trim(parts[1], "\"'")
						}
					}
					if locCreated["id"] == nil {
						http.Error(w, "ORP did not return the created location id", 502)
						return
					}
					if e := c.request("PUT", path+"/http/servers/"+created.ID+"/locations/"+fmt.Sprint(locCreated["id"])+"/directives", fields, nil); e != nil {
						http.Error(w, "set location action "+loc.Path+": "+e.Error(), 502)
						return
					}
				}
			}
		}
	}
	now := time.Now().UTC()
	b.ImportedAt = &now
	b.ImportCenter = input.CenterID
	a.mu.Lock()
	a.batches[id] = b
	err = a.persist(b)
	a.mu.Unlock()
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	write(w, 200, map[string]any{"batch": b, "createdServers": count, "unsupportedCount": len(b.Preview.Unsupported), "message": "Converted configuration was imported into ORP; inspect unsupported directives before publishing."})
}
func proxyUpstream(pass string) string {
	v := strings.TrimSpace(strings.TrimPrefix(pass, "http://"))
	v = strings.TrimSpace(strings.TrimPrefix(v, "https://"))
	v = strings.SplitN(v, "/", 2)[0]
	if strings.Contains(v, ":") {
		return ""
	}
	return v
}
