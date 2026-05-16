// Package inventory parses Ansible-compatible inventory files (INI format)
// and exposes a structured view of hosts and groups.
package inventory

import (
	"bufio"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Host represents a single target host with its connection parameters and variables.
type Host struct {
	Name string
	Vars map[string]string
}

// GetVar returns the value of a host variable, or the provided default.
func (h *Host) GetVar(key, def string) string {
	if v, ok := h.Vars[key]; ok {
		return v
	}
	return def
}

// Group is a named collection of hosts.
type Group struct {
	Name  string
	Hosts []*Host
	Vars  map[string]string
}

// Inventory is the top-level structure holding all parsed hosts and groups.
type Inventory struct {
	Hosts  map[string]*Host
	Groups map[string]*Group
}

// New creates an empty inventory.
func New() *Inventory {
	return &Inventory{
		Hosts:  map[string]*Host{},
		Groups: map[string]*Group{"all": {Name: "all", Hosts: []*Host{}, Vars: map[string]string{}}},
	}
}

// ParseFile reads an Ansible INI inventory file and returns a populated Inventory.
func ParseFile(path string) (*Inventory, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("inventory: open %q: %w", path, err)
	}
	defer f.Close()

	inv := New()
	currentGroup := inv.Groups["all"]
	inVarsSection := false

	rangeRe := regexp.MustCompile(`\[(\w[\w.-]*)\[(\d+):(\d+)\](\w[\w.-]*)?\]`)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}

		// Section header: [groupname] or [groupname:vars] or [groupname:children]
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sectionName := line[1 : len(line)-1]
			inVarsSection = false

			if strings.HasSuffix(sectionName, ":vars") {
				groupName := strings.TrimSuffix(sectionName, ":vars")
				currentGroup = ensureGroup(inv, groupName)
				inVarsSection = true
			} else if strings.HasSuffix(sectionName, ":children") {
				// children sections handled by further [group] sections; skip for now
				currentGroup = ensureGroup(inv, strings.TrimSuffix(sectionName, ":children"))
			} else {
				currentGroup = ensureGroup(inv, sectionName)
			}
			continue
		}

		if inVarsSection {
			// key=value pairs that apply to the group
			k, v := parseKeyVal(line)
			if k != "" {
				currentGroup.Vars[k] = v
			}
			continue
		}

		// Expand range patterns like web[01:03].example.com
		hosts := expandRange(line, rangeRe)
		for _, rawHost := range hosts {
			parts := strings.Fields(rawHost)
			hostName := parts[0]

			host, exists := inv.Hosts[hostName]
			if !exists {
				host = &Host{Name: hostName, Vars: map[string]string{}}
				inv.Hosts[hostName] = host
			}
			// Inline vars: hostname key=val key2=val2
			for _, kv := range parts[1:] {
				k, v := parseKeyVal(kv)
				if k != "" {
					host.Vars[k] = v
				}
			}
			currentGroup.Hosts = append(currentGroup.Hosts, host)
			if currentGroup.Name != "all" {
				// Add to "all" group only when not already in it via currentGroup.
				if !groupContainsHost(inv.Groups["all"], host) {
					inv.Groups["all"].Hosts = append(inv.Groups["all"].Hosts, host)
				}
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("inventory: scan: %w", err)
	}
	return inv, nil
}

// ParseString parses an inventory from a raw string (useful for testing).
func ParseString(content string) (*Inventory, error) {
	// Write to a temp file and delegate to ParseFile-like logic via strings.Reader.
	inv := New()
	currentGroup := inv.Groups["all"]
	inVarsSection := false
	rangeRe := regexp.MustCompile(`\[(\w[\w.-]*)\[(\d+):(\d+)\](\w[\w.-]*)?\]`)

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			sectionName := line[1 : len(line)-1]
			inVarsSection = false
			if strings.HasSuffix(sectionName, ":vars") {
				currentGroup = ensureGroup(inv, strings.TrimSuffix(sectionName, ":vars"))
				inVarsSection = true
			} else if strings.HasSuffix(sectionName, ":children") {
				currentGroup = ensureGroup(inv, strings.TrimSuffix(sectionName, ":children"))
			} else {
				currentGroup = ensureGroup(inv, sectionName)
			}
			continue
		}
		if inVarsSection {
			k, v := parseKeyVal(line)
			if k != "" {
				currentGroup.Vars[k] = v
			}
			continue
		}
		for _, rawHost := range expandRange(line, rangeRe) {
			parts := strings.Fields(rawHost)
			hostName := parts[0]
			host, exists := inv.Hosts[hostName]
			if !exists {
				host = &Host{Name: hostName, Vars: map[string]string{}}
				inv.Hosts[hostName] = host
			}
			for _, kv := range parts[1:] {
				k, v := parseKeyVal(kv)
				if k != "" {
					host.Vars[k] = v
				}
			}
			currentGroup.Hosts = append(currentGroup.Hosts, host)
			if currentGroup.Name != "all" {
				if !groupContainsHost(inv.Groups["all"], host) {
					inv.Groups["all"].Hosts = append(inv.Groups["all"].Hosts, host)
				}
			}
		}
	}
	return inv, scanner.Err()
}

// MatchPattern resolves an Ansible host pattern (e.g. "all", "webservers",
// "web1.example.com") to a slice of matching hosts.
func (inv *Inventory) MatchPattern(pattern string) ([]*Host, error) {
	if pattern == "all" || pattern == "*" {
		return inv.Groups["all"].Hosts, nil
	}

	// Direct host lookup
	if h, ok := inv.Hosts[pattern]; ok {
		return []*Host{h}, nil
	}

	// Group lookup
	if g, ok := inv.Groups[pattern]; ok {
		return g.Hosts, nil
	}

	return nil, fmt.Errorf("inventory: no hosts matched pattern %q", pattern)
}

// groupContainsHost returns true if the group already contains the given host.
func groupContainsHost(g *Group, h *Host) bool {
	for _, existing := range g.Hosts {
		if existing == h {
			return true
		}
	}
	return false
}

// ensureGroup returns the named group, creating it if it doesn't exist.
func ensureGroup(inv *Inventory, name string) *Group {
	if g, ok := inv.Groups[name]; ok {
		return g
	}
	g := &Group{Name: name, Hosts: []*Host{}, Vars: map[string]string{}}
	inv.Groups[name] = g
	return g
}

// parseKeyVal splits "key=value" into ("key", "value").
func parseKeyVal(s string) (string, string) {
	idx := strings.IndexByte(s, '=')
	if idx < 0 {
		return "", ""
	}
	return strings.TrimSpace(s[:idx]), strings.TrimSpace(s[idx+1:])
}

// expandRange expands Ansible host range syntax: web[01:03] → web01, web02, web03.
func expandRange(line string, re *regexp.Regexp) []string {
	m := re.FindStringSubmatch(line)
	if m == nil {
		return []string{line}
	}
	prefix := m[1]
	start, _ := strconv.Atoi(m[2])
	end, _ := strconv.Atoi(m[3])
	suffix := m[4]
	width := len(m[2]) // zero-pad width from start token

	var result []string
	for i := start; i <= end; i++ {
		result = append(result, fmt.Sprintf("%s%0*d%s", prefix, width, i, suffix))
	}
	return result
}
