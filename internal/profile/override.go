// The override's keys and the order Merge applies them in: Merge in merge.go. The GUI edits it
// through the functions below, on its YAML tree so the user's comments and layout survive.

package profile

import (
	"errors"
	"fmt"
	"slices"

	"go.yaml.in/yaml/v3"
)

// PrependRule puts rule first in the override's prepend-rules, keeping the rest of the override,
// comments included, as the user wrote it.
func (s *Store) PrependRule(rule string) error {
	return s.editOverride(func(root *yaml.Node) error {
		seq := child(root, "prepend-rules", yaml.SequenceNode)
		if seq.Kind != yaml.SequenceNode {
			return errors.New("覆写中的 prepend-rules 不是列表")
		}
		seq.Content = append([]*yaml.Node{{Kind: yaml.ScalarNode, Value: rule}}, seq.Content...)
		return nil
	})
}

// blockStyle writes every non-empty list and mapping one item per line: a flow-style
// `prepend-proxies: [{name: VPN, ...}]` (as an edit once left it) is unreadable in the editor.
func blockStyle(n *yaml.Node) {
	if (n.Kind == yaml.SequenceNode || n.Kind == yaml.MappingNode) && len(n.Content) > 0 {
		n.Style &^= yaml.FlowStyle
	}
	for _, c := range n.Content {
		blockStyle(c)
	}
}

// User items are what the GUI adds into the override: they survive subscription updates.

// UserItems lists the GUI-editable parts of the override.
type UserItems struct {
	Proxies []map[string]any `json:"proxies"` // prepend-proxies
	Groups  []map[string]any `json:"groups"`  // prepend-proxy-groups
	Rules   []string         `json:"rules"`   // prepend-rules
	Tunnels []map[string]any `json:"tunnels"` // reverse-tunnels
	Members map[string][]any `json:"members"` // group-members: group -> user proxies prepended into it
}

func (s *Store) UserItems() (UserItems, error) {
	text, err := s.Override()
	if err != nil {
		return UserItems{}, err
	}
	var raw struct {
		Proxies []map[string]any `yaml:"prepend-proxies"`
		Groups  []map[string]any `yaml:"prepend-proxy-groups"`
		Rules   []string         `yaml:"prepend-rules"`
		Tunnels []map[string]any `yaml:"reverse-tunnels"`
		Members map[string][]any `yaml:"group-members"`
	}
	if err := yaml.Unmarshal([]byte(text), &raw); err != nil {
		return UserItems{}, err
	}
	return UserItems(raw), nil
}

// EditProxy changes the named proxy by patch (only the edited fields; a null removes one). A
// GUI-added proxy is patched in place; one from the profile gets the patch stored under
// patch-proxies, which Merge lays over it, so the edit survives subscription updates while the
// fields left alone keep following the subscription.
func (s *Store) EditProxy(name string, patch map[string]any) error {
	return s.editOverride(func(root *yaml.Node) error {
		seq := listOf(root, "prepend-proxies")
		if i := findNamed(seq, name); i >= 0 {
			var proxy map[string]any
			if err := seq.Content[i].Decode(&proxy); err != nil {
				return err
			}
			applyPatch(proxy, patch)
			if err := seq.Content[i].Encode(proxy); err != nil {
				return err
			}
			orderKeys(seq.Content[i])
			return nil
		}
		patches := child(root, "patch-proxies", yaml.MappingNode)
		if patches.Kind != yaml.MappingNode {
			return errors.New("覆写中的 patch-proxies 不是字典")
		}
		n := child(patches, name, yaml.MappingNode)
		stored := map[string]any{}
		if err := n.Decode(&stored); err != nil {
			return err
		}
		return n.Encode(merge(stored, patch)) // nulls kept: they remove the profile's field at Merge
	})
}

// AddProxy adds a node and puts it at the front of each of groups.
func (s *Store) AddProxy(proxy map[string]any, groups []string) error {
	name, _ := proxy["name"].(string)
	if name == "" {
		return errors.New("代理名称不能为空")
	}
	return s.editOverride(func(root *yaml.Node) error {
		if findNamed(listOf(root, "prepend-proxies"), name) >= 0 {
			return fmt.Errorf("已存在名为 %s 的代理", name)
		}
		if err := appendItem(root, "prepend-proxies", proxy); err != nil {
			return err
		}
		members := child(root, "group-members", yaml.MappingNode)
		for _, g := range groups {
			if err := appendItem(members, g, name); err != nil {
				return err
			}
		}
		return nil
	})
}

// RemoveProxy deletes a user node and its group memberships; a profile's node is listed under
// remove-proxies instead, which Merge applies.
func (s *Store) RemoveProxy(name string) error {
	return s.editOverride(func(root *yaml.Node) error {
		if findNamed(listOf(root, "prepend-proxies"), name) < 0 {
			return listName(root, "remove-proxies", name)
		}
		removeNamed(root, "prepend-proxies", name)
		if members := valueOf(root, "group-members"); members != nil && members.Kind == yaml.MappingNode {
			for i := 1; i < len(members.Content); i += 2 {
				seq := members.Content[i]
				seq.Content = slices.DeleteFunc(seq.Content, func(n *yaml.Node) bool { return n.Value == name })
			}
		}
		return nil
	})
}

func (s *Store) AddGroup(group map[string]any) error {
	name, _ := group["name"].(string)
	if name == "" {
		return errors.New("策略组名称不能为空")
	}
	return s.editOverride(func(root *yaml.Node) error {
		if findNamed(listOf(root, "prepend-proxy-groups"), name) >= 0 {
			return fmt.Errorf("已存在名为 %s 的策略组", name)
		}
		return appendItem(root, "prepend-proxy-groups", group)
	})
}

// EditGroup replaces the named group with group, its name kept (rules and groups refer to it). A
// user group is replaced in place; a profile's is kept whole in replace-proxy-groups, which Merge
// swaps in, so the edit survives subscription updates.
func (s *Store) EditGroup(name string, group map[string]any) error {
	group["name"] = name
	var n yaml.Node
	if err := n.Encode(group); err != nil {
		return err
	}
	orderKeys(&n)
	return s.editOverride(func(root *yaml.Node) error {
		if seq := listOf(root, "prepend-proxy-groups"); findNamed(seq, name) >= 0 {
			seq.Content[findNamed(seq, name)] = &n
			return nil
		}
		groups := child(root, "replace-proxy-groups", yaml.MappingNode)
		if groups.Kind != yaml.MappingNode {
			return errors.New("覆写中的 replace-proxy-groups 不是字典")
		}
		*child(groups, name, yaml.MappingNode) = n
		return nil
	})
}

// RemoveGroup deletes a user group, or lists a profile's group under remove-proxy-groups.
func (s *Store) RemoveGroup(name string) error {
	return s.editOverride(func(root *yaml.Node) error {
		if findNamed(listOf(root, "prepend-proxy-groups"), name) < 0 {
			return listName(root, "remove-proxy-groups", name)
		}
		removeNamed(root, "prepend-proxy-groups", name)
		return nil
	})
}

// listName adds name to the override's string list key, once.
func listName(root *yaml.Node, key, name string) error {
	if seq := listOf(root, key); seq != nil && slices.ContainsFunc(seq.Content, func(n *yaml.Node) bool { return n.Value == name }) {
		return nil
	}
	return appendItem(root, key, name)
}

func (s *Store) AddTunnel(tunnel map[string]any) error {
	name, _ := tunnel["name"].(string)
	if name == "" {
		return errors.New("隧道名称不能为空")
	}
	return s.editOverride(func(root *yaml.Node) error {
		if findNamed(listOf(root, "reverse-tunnels"), name) >= 0 {
			return fmt.Errorf("已存在名为 %s 的隧道", name)
		}
		return appendItem(root, "reverse-tunnels", tunnel)
	})
}

// EditTunnel replaces the GUI-added tunnel named name with tunnel, in place; it may rename it.
func (s *Store) EditTunnel(name string, tunnel map[string]any) error {
	newName, _ := tunnel["name"].(string)
	if newName == "" {
		return errors.New("隧道名称不能为空")
	}
	return s.editOverride(func(root *yaml.Node) error {
		seq := listOf(root, "reverse-tunnels")
		i := findNamed(seq, name)
		if i < 0 {
			return fmt.Errorf("没有名为 %s 的隧道", name)
		}
		if j := findNamed(seq, newName); j >= 0 && j != i {
			return fmt.Errorf("已存在名为 %s 的隧道", newName)
		}
		var n yaml.Node
		if err := n.Encode(tunnel); err != nil {
			return err
		}
		orderKeys(&n)
		seq.Content[i] = &n
		return nil
	})
}

// SetTunnelDisabled stops a tunnel or starts it again: `disabled: true` on its entry, or no
// disabled key. The core lists a disabled tunnel as stopped and doesn't run it.
func (s *Store) SetTunnelDisabled(name string, disabled bool) error {
	return s.editOverride(func(root *yaml.Node) error {
		seq := listOf(root, "reverse-tunnels")
		i := findNamed(seq, name)
		if i < 0 {
			return fmt.Errorf("没有名为 %s 的隧道", name)
		}
		m := seq.Content[i]
		for j := 0; j+1 < len(m.Content); j += 2 {
			if m.Content[j].Value == "disabled" {
				m.Content = append(m.Content[:j], m.Content[j+2:]...)
				break
			}
		}
		if disabled {
			m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: "disabled"}, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!bool", Value: "true"})
		}
		return nil
	})
}

func (s *Store) RemoveTunnel(name string) error {
	return s.editOverride(func(root *yaml.Node) error {
		removeNamed(root, "reverse-tunnels", name)
		return nil
	})
}

// MoveRule swaps the GUI-added rule at index (in prepend-rules) with the one delta places away.
// The profile's own rules keep their order, so subscription updates still reach them.
func (s *Store) MoveRule(index, delta int) error {
	return s.editOverride(func(root *yaml.Node) error {
		seq, j := listOf(root, "prepend-rules"), index+delta
		if seq == nil || min(index, j) < 0 || max(index, j) >= len(seq.Content) {
			return errors.New("只能调整自己添加的规则的顺序")
		}
		seq.Content[index], seq.Content[j] = seq.Content[j], seq.Content[index]
		return nil
	})
}

// RemoveRule drops a GUI-added rule from the override.
func (s *Store) RemoveRule(rule string) error {
	return s.editOverride(func(root *yaml.Node) error {
		if seq := listOf(root, "prepend-rules"); seq != nil {
			seq.Content = slices.DeleteFunc(seq.Content, func(n *yaml.Node) bool { return n.Value == rule })
		}
		return nil
	})
}

// HideRule drops one of the profile's rules by listing it under remove-rules, which Merge filters
// out; the rest of the profile's rules still follow the subscription.
func (s *Store) HideRule(rule string) error {
	return s.editOverride(func(root *yaml.Node) error { return appendItem(root, "remove-rules", rule) })
}

// editOverride applies change to the override's YAML tree, keeping comments and layout.
func (s *Store) editOverride(change func(root *yaml.Node) error) error {
	text, err := s.Override()
	if err != nil {
		return err
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return err
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return errors.New("覆写不是 YAML 字典")
	}
	if err := change(root); err != nil {
		return err
	}
	tidy(root)
	blockStyle(&doc)
	b, err := yaml.Marshal(&doc)
	if err != nil {
		return err
	}
	return s.SetOverride(string(b))
}

func valueOf(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

// child returns m[key], creating it with kind when missing.
func child(m *yaml.Node, key string, kind yaml.Kind) *yaml.Node {
	if v := valueOf(m, key); v != nil {
		return v
	}
	v := &yaml.Node{Kind: kind}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: key}, v)
	return v
}

func listOf(m *yaml.Node, key string) *yaml.Node {
	if v := valueOf(m, key); v != nil && v.Kind == yaml.SequenceNode {
		return v
	}
	return nil
}

func appendItem(m *yaml.Node, key string, item any) error {
	seq := child(m, key, yaml.SequenceNode)
	if seq.Kind != yaml.SequenceNode {
		return fmt.Errorf("覆写中的 %s 不是列表", key)
	}
	var n yaml.Node
	if err := n.Encode(item); err != nil {
		return err
	}
	orderKeys(&n)
	seq.Content = append(seq.Content, &n)
	return nil
}

// leadingKeys come first in a node or group the GUI writes; the rest follow alphabetically, as the
// encoder sorts a map. Only readability: the core doesn't care about the order.
var leadingKeys = []string{"name", "type", "server", "port", "username", "password"}

func orderKeys(n *yaml.Node) {
	if n.Kind != yaml.MappingNode {
		return
	}
	out := make([]*yaml.Node, 0, len(n.Content))
	for _, key := range leadingKeys { // leading keys first, in that order
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				out = append(out, n.Content[i], n.Content[i+1])
			}
		}
	}
	for i := 0; i+1 < len(n.Content); i += 2 { // then the rest, as encoded (alphabetical)
		if !slices.Contains(leadingKeys, n.Content[i].Value) {
			out = append(out, n.Content[i], n.Content[i+1])
		}
	}
	n.Content = out
}

// findNamed returns the index of the mapping with name in seq, or -1.
func findNamed(seq *yaml.Node, name string) int {
	if seq == nil {
		return -1
	}
	return slices.IndexFunc(seq.Content, func(n *yaml.Node) bool {
		v := valueOf(n, "name")
		return n.Kind == yaml.MappingNode && v != nil && v.Value == name
	})
}

func removeNamed(m *yaml.Node, key, name string) {
	if seq := listOf(m, key); seq != nil {
		if i := findNamed(seq, name); i >= 0 {
			seq.Content = slices.Delete(seq.Content, i, i+1)
		}
	}
}

// SetPath sets a nested key such as dns.enhanced-mode in the override, creating the maps on the
// way and leaving sibling keys and comments alone.
func (s *Store) SetPath(path []string, value any) error {
	if len(path) == 0 {
		return errors.New("empty path")
	}
	return s.editOverride(func(root *yaml.Node) error {
		m := root
		for _, key := range path[:len(path)-1] {
			m = child(m, key, yaml.MappingNode)
			if m.Kind != yaml.MappingNode {
				return fmt.Errorf("覆写中的 %s 不是字典", key)
			}
		}
		var n yaml.Node
		if err := n.Encode(value); err != nil {
			return err
		}
		last := path[len(path)-1]
		if v := valueOf(m, last); v != nil {
			*v = n
			return nil
		}
		m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: last}, &n)
		return nil
	})
}

// guiKeys are the override keys the GUI writes; tidy only touches these, a key the user wrote stays.
var guiKeys = []string{"prepend-proxies", "prepend-proxy-groups", "prepend-rules", "append-proxies", "append-proxy-groups", "append-rules",
	"group-members", "patch-proxies", "replace-proxy-groups", "remove-proxies", "remove-proxy-groups", "remove-rules", "reverse-tunnels"}

// tidy drops what an undone edit leaves behind: the patch or replacement of a removed proxy or
// group, a group-members entry with no proxies left, and GUI keys that are now empty.
func tidy(root *yaml.Node) {
	removed := func(key string) []string {
		var names []string
		if seq := listOf(root, key); seq != nil {
			for _, n := range seq.Content {
				names = append(names, n.Value)
			}
		}
		return names
	}
	for key, gone := range map[string][]string{"patch-proxies": removed("remove-proxies"), "replace-proxy-groups": removed("remove-proxy-groups"), "group-members": nil} {
		m := valueOf(root, key)
		if m == nil || m.Kind != yaml.MappingNode {
			continue
		}
		for i := len(m.Content) - 2; i >= 0; i -= 2 {
			if slices.Contains(gone, m.Content[i].Value) || len(m.Content[i+1].Content) == 0 {
				m.Content = slices.Delete(m.Content, i, i+2)
			}
		}
	}
	for i := len(root.Content) - 2; i >= 0; i -= 2 {
		v := root.Content[i+1]
		if slices.Contains(guiKeys, root.Content[i].Value) && (v.Kind == yaml.MappingNode || v.Kind == yaml.SequenceNode) && len(v.Content) == 0 {
			root.Content = slices.Delete(root.Content, i, i+2)
		}
	}
}
