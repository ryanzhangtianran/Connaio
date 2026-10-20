package profile

import (
	"errors"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// The override as two parts: what any Clash (mihomo) client reads, and what only Connaio's core
// does. It is stored as one file, as Merge reads it; the parts are a view the GUI edits, joined
// back with the Connaio part's items first.

// connaioKeys are the top-level keys only Connaio's core reads.
var connaioKeys = []string{"reverse-tunnels", "capture"}

// connaioRules are the rule types only Connaio's core knows.
var connaioRules = []string{"EASYCONNECT", "PROCESS-ANCESTOR", "PROCESS-ANCESTOR-PATH"}

// connaioNames are the proxies and groups a Clash client couldn't load: EasyConnect nodes, another
// Connaio on the LAN, subnet groups, and every group that lists one of them, in the config as
// merged (the profile's included: a subscription can carry an EasyConnect node).
func connaioNames(cfg map[string]any) map[string]bool {
	names := map[string]bool{}
	proxies, _ := cfg["proxies"].([]any)
	for _, x := range proxies {
		p, _ := x.(map[string]any)
		if lan, _ := p["connaio-lan"].(bool); p["type"] == "easyconnect" || lan {
			names[str(p["name"])] = true
		}
	}
	groups, _ := cfg["proxy-groups"].([]any)
	for changed := true; changed; { // a group of a group of such a node can't load either
		changed = false
		for _, x := range groups {
			g, _ := x.(map[string]any)
			name := str(g["name"])
			if names[name] {
				continue
			}
			members, _ := g["proxies"].([]any)
			if g["type"] == "subnet" || slices.ContainsFunc(members, func(m any) bool { return names[str(m)] }) {
				names[name], changed = true, true
			}
		}
	}
	return names
}

func str(v any) string { s, _ := v.(string); return s }

// connaioItem says whether one item of the override's list or named map under key is Connaio's.
func connaioItem(key string, item *yaml.Node, names map[string]bool) bool {
	switch {
	case strings.HasSuffix(key, "rules"): // rules, prepend-, append-, remove-rules: lines
		typ, _, _ := strings.Cut(item.Value, ",")
		return slices.Contains(connaioRules, strings.ToUpper(strings.TrimSpace(typ))) || names[RuleTarget(item.Value)]
	case item.Kind == yaml.ScalarNode: // remove-proxies, remove-proxy-groups: names
		return names[item.Value]
	case item.Kind == yaml.MappingNode: // a node or group
		v := func(k string) string {
			n := valueOf(item, k)
			if n == nil {
				return ""
			}
			return n.Value
		}
		return names[v("name")] || v("type") == "easyconnect" || v("type") == "subnet" || v("connaio-lan") == "true"
	case item.Kind == yaml.SequenceNode: // group-members: a group's added members
		return slices.ContainsFunc(item.Content, func(m *yaml.Node) bool { return names[m.Value] })
	}
	return false
}

// SplitOverride is the override as its Clash part and its Connaio part, comments kept with what
// they annotate.
func (s *Store) SplitOverride() (clash, connaio string, err error) {
	text, err := s.Override()
	if err != nil {
		return "", "", err
	}
	root, err := parseRoot(text)
	if err != nil {
		return "", "", err
	}
	cfg, err := s.merged(false)
	if err != nil {
		return "", "", err
	}
	names := connaioNames(cfg)
	c, x := &yaml.Node{Kind: yaml.MappingNode}, &yaml.Node{Kind: yaml.MappingNode}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		switch {
		case slices.Contains(connaioKeys, k.Value):
			x.Content = append(x.Content, k, v)
		case k.Value == "dns" && v.Kind == yaml.MappingNode:
			mine, theirs := &yaml.Node{Kind: yaml.MappingNode}, &yaml.Node{Kind: yaml.MappingNode, Style: v.Style}
			for j := 0; j+1 < len(v.Content); j += 2 {
				to := theirs
				if v.Content[j].Value == "use-system-resolvers" {
					to = mine
				}
				to.Content = append(to.Content, v.Content[j], v.Content[j+1])
			}
			addPart(c, k, theirs)
			addPart(x, &yaml.Node{Kind: yaml.ScalarNode, Value: "dns"}, mine)
		case v.Kind == yaml.SequenceNode:
			mine, theirs := splitNode(v, func(n *yaml.Node) bool { return connaioItem(k.Value, n, names) }, 1)
			addPart(c, k, theirs)
			addPart(x, &yaml.Node{Kind: yaml.ScalarNode, Value: k.Value, HeadComment: k.HeadComment}, mine)
		case v.Kind == yaml.MappingNode && slices.Contains([]string{"patch-proxies", "replace-proxy-groups", "group-members"}, k.Value):
			mine, theirs := splitNode(v, func(n *yaml.Node) bool { return false }, 2, func(name, val *yaml.Node) bool {
				return names[name.Value] || connaioItem(k.Value, val, names)
			})
			addPart(c, k, theirs)
			addPart(x, &yaml.Node{Kind: yaml.ScalarNode, Value: k.Value}, mine)
		default:
			c.Content = append(c.Content, k, v)
		}
	}
	return encodeRoot(c), encodeRoot(x), nil
}

// splitNode parts a list (step 1) or a named map (step 2) into the items mine says are Connaio's
// and the rest; a map's entries are judged by pair.
func splitNode(n *yaml.Node, mine func(*yaml.Node) bool, step int, pair ...func(k, v *yaml.Node) bool) (connaio, clash *yaml.Node) {
	connaio, clash = &yaml.Node{Kind: n.Kind, Style: n.Style}, &yaml.Node{Kind: n.Kind, Style: n.Style}
	for i := 0; i+step-1 < len(n.Content); i += step {
		item := n.Content[i : i+step]
		to := clash
		if step == 1 && mine(item[0]) || step == 2 && pair[0](item[0], item[1]) {
			to = connaio
		}
		to.Content = append(to.Content, item...)
	}
	return connaio, clash
}

// addPart puts key: value into m unless value is an empty list or map.
func addPart(m, key, value *yaml.Node) {
	if (value.Kind == yaml.SequenceNode || value.Kind == yaml.MappingNode) && len(value.Content) == 0 {
		return
	}
	m.Content = append(m.Content, key, value)
}

// JoinOverride stores the two parts as the override: Connaio's laid over Clash's, its list items
// first (its rules thus ahead, as the GUI's VPN and tunnel rules already are).
func (s *Store) JoinOverride(clash, connaio string) error {
	c, err := parseRoot(clash)
	if err != nil {
		return errors.New("Clash 部分不是有效的 YAML 字典")
	}
	x, err := parseRoot(connaio)
	if err != nil {
		return errors.New("Connaio 部分不是有效的 YAML 字典")
	}
	return s.SetOverride(encodeRoot(mergeNode(c, x, true)))
}

// ImportOverride lays the YAML mapping text over the override: a key in both is merged further,
// a list gets the imported items after its own, one with the same name or text replacing its own.
func (s *Store) ImportOverride(text string) error {
	in, err := parseRoot(text)
	if err != nil {
		return errors.New("导入的文件不是有效的 YAML 字典")
	}
	current, err := s.Override()
	if err != nil {
		return err
	}
	root, err := parseRoot(current)
	if err != nil {
		return err
	}
	return s.SetOverride(encodeRoot(mergeNode(root, in, false)))
}

// mergeNode lays over onto base: mappings key by key, lists item by item (over's first or last; a
// base item with the same identity gives way to over's), anything else over's.
func mergeNode(base, over *yaml.Node, overFirst bool) *yaml.Node {
	switch {
	case base.Kind == yaml.MappingNode && over.Kind == yaml.MappingNode:
		out := &yaml.Node{Kind: yaml.MappingNode, Style: base.Style, HeadComment: base.HeadComment}
		out.Content = append(out.Content, base.Content...)
		for i := 0; i+1 < len(over.Content); i += 2 {
			k, v := over.Content[i], over.Content[i+1]
			j := slices.IndexFunc(out.Content, func(n *yaml.Node) bool { return n.Value == k.Value })
			if j%2 == 0 && j >= 0 { // a key, not a value that happens to be spelt the same
				out.Content[j+1] = mergeNode(out.Content[j+1], v, overFirst)
			} else {
				out.Content = append(out.Content, k, v)
			}
		}
		return out
	case base.Kind == yaml.SequenceNode && over.Kind == yaml.SequenceNode:
		out := &yaml.Node{Kind: yaml.SequenceNode, Style: base.Style}
		taken := map[string]bool{}
		for _, n := range over.Content {
			taken[identity(n)] = true
		}
		kept := slices.DeleteFunc(slices.Clone(base.Content), func(n *yaml.Node) bool { return taken[identity(n)] })
		if overFirst {
			out.Content = append(append(out.Content, over.Content...), kept...)
		} else {
			out.Content = append(append(out.Content, kept...), over.Content...)
		}
		return out
	}
	return over
}

// identity is what makes two list items the same one: a node's or group's name, else its text.
func identity(n *yaml.Node) string {
	if n.Kind == yaml.MappingNode {
		if name := valueOf(n, "name"); name != nil {
			return "name:" + name.Value
		}
	}
	b, _ := yaml.Marshal(n)
	return string(b)
}

// parseRoot reads a YAML mapping (empty text: an empty one).
func parseRoot(text string) (*yaml.Node, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 {
		return &yaml.Node{Kind: yaml.MappingNode}, nil
	}
	if root := doc.Content[0]; root.Kind == yaml.MappingNode {
		return root, nil
	}
	return nil, errors.New("not a YAML mapping")
}

// encodeRoot writes a mapping as the override is kept: block style, "" for an empty one.
func encodeRoot(root *yaml.Node) string {
	if len(root.Content) == 0 {
		return ""
	}
	doc := &yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{root}}
	blockStyle(doc)
	b, _ := yaml.Marshal(doc)
	return string(b)
}
