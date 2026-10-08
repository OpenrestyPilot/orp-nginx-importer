package importer

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

type token struct {
	text string
	line int
}
type node struct {
	name     string
	args     []token
	children []*node
	line     int
	file     string
}

// parseDump reads nginx -T output without executing or including any referenced file.
func parseDump(src string) (*node, []Unsupported) {
	root := &node{name: "root", file: "<dump>"}
	stack := []*node{root}
	file := "<dump>"
	line := 0
	var toks []token
	var unsupported []Unsupported
	flush := func(end string) {
		if len(toks) == 0 {
			return
		}
		parent := stack[len(stack)-1]
		n := &node{name: toks[0].text, line: toks[0].line, file: file}
		n.args = append(n.args, toks[1:]...)
		if end == "{" {
			parent.children = append(parent.children, n)
			stack = append(stack, n)
		} else {
			parent.children = append(parent.children, n)
		}
		toks = nil
	}
	s := bufio.NewScanner(strings.NewReader(src))
	s.Buffer(make([]byte, 4096), 4*1024*1024)
	for s.Scan() {
		line++
		raw := s.Text()
		trim := strings.TrimSpace(raw)
		if strings.HasPrefix(trim, "nginx version:") || strings.HasPrefix(trim, "built by ") || strings.HasPrefix(trim, "configure arguments:") {
			toks = nil
			continue
		}
		if strings.HasPrefix(trim, "# configuration file ") && strings.HasSuffix(trim, ":") {
			file = strings.TrimSuffix(strings.TrimPrefix(trim, "# configuration file "), ":")
			continue
		}
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		parts, err := lexLine(raw, line)
		if err != nil {
			unsupported = append(unsupported, Unsupported{File: file, Context: context(stack), Line: line, Directive: trim, Reason: err.Error()})
			continue
		}
		for i := 0; i < len(parts); i++ {
			p := parts[i]
			switch p.text {
			case "{":
				flush("{")
			case ";":
				flush(";")
			case "}":
				flush(";")
				if len(stack) > 1 {
					stack = stack[:len(stack)-1]
				} else {
					unsupported = append(unsupported, Unsupported{File: file, Context: "root", Line: line, Directive: "}", Reason: "unexpected closing brace"})
				}
			default:
				toks = append(toks, p)
			}
		}
		if len(toks) > 0 {
			toks = append(toks, token{text: "\n", line: line})
		}
	}
	if len(toks) > 0 {
		flush(";")
	}
	if err := s.Err(); err != nil {
		unsupported = append(unsupported, Unsupported{File: file, Context: context(stack), Line: line, Reason: err.Error()})
	}
	if len(stack) > 1 {
		for _, n := range stack[1:] {
			unsupported = append(unsupported, Unsupported{File: n.file, Context: context(stack), Line: n.line, Directive: n.name, Reason: "unclosed configuration block"})
		}
	}
	return root, unsupported
}

func lexLine(raw string, line int) ([]token, error) {
	var out []token
	var b strings.Builder
	quote := rune(0)
	escaped := false
	flush := func() {
		if b.Len() > 0 {
			out = append(out, token{text: b.String(), line: line})
			b.Reset()
		}
	}
	for _, r := range raw {
		if escaped {
			b.WriteRune(r)
			escaped = false
			continue
		}
		if r == '\\' {
			escaped = true
			continue
		}
		if quote != 0 {
			if r == quote {
				quote = 0
			} else {
				b.WriteRune(r)
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == '#' {
			flush()
			break
		}
		if r == '{' || r == '}' || r == ';' {
			flush()
			out = append(out, token{text: string(r), line: line})
			continue
		}
		if r == ' ' || r == '\t' || r == '\r' {
			flush()
			continue
		}
		b.WriteRune(r)
	}
	if escaped {
		b.WriteRune('\\')
	}
	if quote != 0 {
		return out, fmt.Errorf("unterminated quoted string")
	}
	flush()
	return out, nil
}
func context(s []*node) string {
	names := make([]string, 0, len(s))
	for _, n := range s {
		if n.name != "root" {
			names = append(names, n.name)
		}
	}
	if len(names) == 0 {
		return "root"
	}
	return strings.Join(names, " > ")
}
func arg(n *node, i int) string {
	if i >= len(n.args) {
		return ""
	}
	return n.args[i].text
}
func number(s string, fallback int) int {
	n, e := strconv.Atoi(s)
	if e != nil {
		return fallback
	}
	return n
}

func convert(root *node, issues []Unsupported) Preview {
	p := Preview{Upstreams: []Upstream{}, Servers: []Server{}, Unsupported: append([]Unsupported{}, issues...)}
	upstreams := map[string]bool{}
	var upstreamBlocks []*node
	for _, f := range root.children {
		if f.name == "upstream" {
			upstreamBlocks = append(upstreamBlocks, f)
		}
		if f.name == "http" {
			for _, child := range f.children {
				if child.name == "upstream" {
					upstreamBlocks = append(upstreamBlocks, child)
				}
			}
		}
	}
	for _, f := range upstreamBlocks {
		name := arg(f, 0)
		if name == "" {
			continue
		}
		upstreams[name] = true
		u := Upstream{Name: name, Targets: []Target{}}
		for _, d := range f.children {
			if d.name == "server" {
				host, port := splitAddress(arg(d, 0))
				if host == "" || port == 0 {
					p.Unsupported = append(p.Unsupported, Unsupported{File: d.file, Context: "upstream " + name, Line: d.line, Directive: "server", Reason: "cannot parse upstream target address"})
					continue
				}
				t := Target{Host: host, Port: port, Weight: 1}
				for i := 1; i < len(d.args); i++ {
					a := d.args[i].text
					if strings.HasPrefix(a, "weight=") {
						t.Weight = number(strings.TrimPrefix(a, "weight="), 1)
					}
					if a == "backup" {
						t.Backup = true
					}
				}
				u.Targets = append(u.Targets, t)
			} else {
				p.Unsupported = append(p.Unsupported, unsupportedNode(d, "upstream "+name, "directive is not represented by ORP upstream model"))
			}
		}
		p.Upstreams = append(p.Upstreams, u)
	}
	for _, h := range root.children {
		if h.name != "http" {
			continue
		}
		for _, s := range h.children {
			if s.name != "server" {
				if s.name != "upstream" {
					p.Unsupported = append(p.Unsupported, unsupportedNode(s, "http", "only server and upstream blocks are converted"))
				}
				continue
			}
			server := Server{Names: []string{}, ListenPort: 80, Locations: []Location{}}
			for _, d := range s.children {
				switch d.name {
				case "listen":
					port := number(arg(d, 0), 80)
					server.ListenPort = port
					for _, a := range d.args {
						if a.text == "ssl" {
							server.TLS = true
						}
					}
					if server.TLS {
						p.Unsupported = append(p.Unsupported, unsupportedNode(d, "http > server", "TLS listener requires manual certificate and TLS policy mapping; imported listener remains HTTP"))
					}
				case "server_name":
					for _, a := range d.args {
						server.Names = append(server.Names, a.text)
					}
				case "location":
					loc := Location{Path: arg(d, 0), Directives: []Directive{}}
					if strings.HasPrefix(loc.Path, "=") {
						loc.Modifier = "="
						loc.Path = strings.TrimPrefix(loc.Path, "=")
					} else if strings.HasPrefix(loc.Path, "~*") {
						loc.Modifier = "~*"
						loc.Path = strings.TrimSpace(strings.TrimPrefix(loc.Path, "~*"))
					} else if strings.HasPrefix(loc.Path, "~") {
						loc.Modifier = "~"
						loc.Path = strings.TrimSpace(strings.TrimPrefix(loc.Path, "~"))
					} else if strings.HasPrefix(loc.Path, "^") {
						loc.Modifier = "^~"
						loc.Path = strings.TrimPrefix(loc.Path, "^~")
					}
					if loc.Modifier != "" {
						p.Unsupported = append(p.Unsupported, Unsupported{File: d.file, Context: "http > server > location", Line: d.line, Directive: "location " + loc.Modifier, Reason: "ORP location resource currently supports prefix paths only; review and adjust the imported path"})
					}
					for _, ld := range d.children {
						switch ld.name {
						case "proxy_pass":
							loc.ProxyPass = arg(ld, 0)
							if strings.Contains(loc.ProxyPass, "$") || strings.HasPrefix(loc.ProxyPass, "https://") || proxyUpstream(loc.ProxyPass) == "" {
								p.Unsupported = append(p.Unsupported, unsupportedNode(ld, "server > location "+loc.Path, "variable, HTTPS, or direct proxy_pass target needs manual mapping"))
							}
						case "root":
							loc.Root = arg(ld, 0)
						case "alias":
							loc.Alias = arg(ld, 0)
						case "return":
							loc.Return = strings.TrimSpace(strings.Join(nodeArgs(ld), " "))
						default:
							loc.Directives = append(loc.Directives, Directive{Name: ld.name, Args: nodeArgs(ld), Line: ld.line})
							p.Unsupported = append(p.Unsupported, unsupportedNode(ld, "server > location "+loc.Path, "preserved as an unsupported directive for manual mapping"))
						}
					}
					server.Locations = append(server.Locations, loc)
				default:
					p.Unsupported = append(p.Unsupported, unsupportedNode(d, "http > server", "directive is not yet mapped to ORP server fields"))
				}
			}
			if len(server.Names) == 0 {
				p.Unsupported = append(p.Unsupported, Unsupported{File: s.file, Context: "http > server", Line: s.line, Directive: "server_name", Reason: "server block has no server_name; assign a domain before import"})
			}
			p.Servers = append(p.Servers, server)
		}
	}
	for _, u := range p.Upstreams {
		if len(u.Targets) == 0 {
			p.Unsupported = append(p.Unsupported, Unsupported{Context: "upstream " + u.Name, Directive: "server", Reason: "upstream has no convertible targets"})
		}
	}
	_ = upstreams
	return p
}
func nodeArgs(n *node) []string {
	a := make([]string, len(n.args))
	for i, v := range n.args {
		a[i] = v.text
	}
	return a
}
func splitAddress(s string) (string, int) {
	if strings.HasPrefix(s, "[") {
		i := strings.LastIndex(s, "]:")
		if i < 0 {
			return "", 0
		}
		return s[1:i], number(s[i+2:], 0)
	}
	i := strings.LastIndex(s, ":")
	if i < 0 {
		return "", 0
	}
	return s[:i], number(s[i+1:], 0)
}
func unsupportedNode(n *node, ctx, reason string) Unsupported {
	return Unsupported{File: n.file, Context: ctx, Line: n.line, Directive: n.name + " " + strings.Join(nodeArgs(n), " "), Reason: reason}
}
