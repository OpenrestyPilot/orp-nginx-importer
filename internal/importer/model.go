package importer

import "time"

type Batch struct {
	ID           string     `json:"id"`
	Name         string     `json:"name"`
	SHA256       string     `json:"sha256"`
	Bytes        int64      `json:"bytes"`
	UploadedAt   time.Time  `json:"uploadedAt"`
	SourceFile   string     `json:"sourceFile"`
	Preview      *Preview   `json:"preview,omitempty"`
	ImportedAt   *time.Time `json:"importedAt,omitempty"`
	ImportCenter string     `json:"importCenter,omitempty"`
}

type Preview struct {
	Upstreams   []Upstream    `json:"upstreams"`
	Servers     []Server      `json:"servers"`
	Unsupported []Unsupported `json:"unsupported"`
}

type Upstream struct {
	Name    string   `json:"name"`
	Targets []Target `json:"targets"`
}
type Target struct {
	Host   string `json:"host"`
	Port   int    `json:"port"`
	Weight int    `json:"weight"`
	Backup bool   `json:"backup"`
}
type Server struct {
	Names      []string   `json:"names"`
	ListenPort int        `json:"listenPort"`
	TLS        bool       `json:"tls"`
	Locations  []Location `json:"locations"`
}
type Location struct {
	Path       string      `json:"path"`
	Modifier   string      `json:"modifier"`
	ProxyPass  string      `json:"proxyPass,omitempty"`
	Root       string      `json:"root,omitempty"`
	Alias      string      `json:"alias,omitempty"`
	Return     string      `json:"return,omitempty"`
	Directives []Directive `json:"directives,omitempty"`
}
type Directive struct {
	Name string   `json:"name"`
	Args []string `json:"args"`
	Line int      `json:"line"`
}
type Unsupported struct {
	File      string `json:"file"`
	Context   string `json:"context"`
	Line      int    `json:"line"`
	Directive string `json:"directive"`
	Reason    string `json:"reason"`
}
