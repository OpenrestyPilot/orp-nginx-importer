package importer

import "testing"

func TestParseDumpPreviewAndUnsupported(t *testing.T) {
	source := `nginx version: nginx/1.26
# configuration file /etc/nginx/conf.d/app.conf:
upstream app_backend {
    server 10.0.0.8:8080 weight=3;
    server [2001:db8::1]:8081 backup;
    keepalive 32;
}
http {
    server {
        listen 8080;
        server_name api.example.test;
        location /v1/ {
            proxy_pass http://app_backend;
            proxy_set_header Host $host;
        }
    }
}`
	root, issues := parseDump(source)
	for _, n := range root.children {
		t.Logf("root node=%s children=%d", n.name, len(n.children))
		for _, c := range n.children {
			t.Logf(" child=%s args=%v children=%d", c.name, nodeArgs(c), len(c.children))
		}
	}
	p := convert(root, issues)
	if len(p.Upstreams) != 1 || len(p.Upstreams[0].Targets) != 2 {
		t.Fatalf("unexpected upstream conversion: %#v", p.Upstreams)
	}
	if p.Upstreams[0].Targets[0].Weight != 3 || !p.Upstreams[0].Targets[1].Backup {
		t.Fatal("upstream attributes were not parsed")
	}
	if len(p.Servers) != 1 || len(p.Servers[0].Locations) != 1 || p.Servers[0].Locations[0].ProxyPass != "http://app_backend" {
		t.Fatalf("unexpected server conversion: %#v", p.Servers)
	}
	if len(p.Unsupported) != 2 {
		t.Fatalf("expected keepalive and proxy_set_header to be reported; got %#v", p.Unsupported)
	}
	if p.Unsupported[0].File != "/etc/nginx/conf.d/app.conf" || p.Unsupported[0].Line == 0 {
		t.Fatalf("source position missing: %#v", p.Unsupported[0])
	}
}

func TestTLSAndVariableProxyAreReported(t *testing.T) {
	root, issues := parseDump("http { server { listen 443 ssl; server_name secure.example; location / { proxy_pass https://$target; } } }")
	p := convert(root, issues)
	if len(p.Unsupported) < 2 {
		t.Fatalf("TLS and variable proxy require manual mapping: %#v", p.Unsupported)
	}
	if !p.Servers[0].TLS {
		t.Fatal("TLS listener was not recognized")
	}
}
