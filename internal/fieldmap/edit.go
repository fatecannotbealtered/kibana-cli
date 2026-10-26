package fieldmap

import (
	"bytes"
	"errors"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

// ErrProfileExists is AddProfile on a name the file already has.
var ErrProfileExists = errors.New("the field-map already has a profile with this name")

// AddProfile returns the field-map file raw with profile p added under name in its
// top-level profiles mapping, created when missing. Comments and the order of
// everything else are kept: the file is edited as a YAML node tree, not decoded and
// re-encoded. raw may be empty (no file yet): the result is then a new file with
// version 1 and the one profile. A name the file already has is ErrProfileExists;
// the file is never changed in place.
func AddProfile(raw []byte, name string, p Profile) ([]byte, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, errors.New("a profile needs a name")
	}
	var doc yaml.Node
	if len(bytes.TrimSpace(raw)) > 0 {
		if err := yaml.Unmarshal(raw, &doc); err != nil {
			return nil, fmt.Errorf("parsing field map: %w", err)
		}
	}
	if doc.Kind == 0 {
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}}}
		root := doc.Content[0]
		root.Content = append(root.Content, scalar("version"), &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!int", Value: "1"})
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return nil, errors.New("the field map is not a YAML mapping")
	}
	root := doc.Content[0]
	profiles := mappingValue(root, "profiles")
	switch {
	case profiles == nil:
		profiles = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		root.Content = append(root.Content, scalar("profiles"), profiles)
	case profiles.Kind == yaml.ScalarNode && (profiles.Tag == "!!null" || profiles.Value == ""):
		*profiles = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	case profiles.Kind != yaml.MappingNode:
		return nil, errors.New("profiles in the field map is not a mapping")
	}
	if mappingValue(profiles, name) != nil {
		return nil, ErrProfileExists
	}
	var value yaml.Node
	if err := value.Encode(p); err != nil {
		return nil, err
	}
	profiles.Content = append(profiles.Content, scalar(name), &value)
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2)
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	// The result must still be a field map this tool reads.
	var check Map
	if err := yaml.Unmarshal(out.Bytes(), &check); err != nil {
		return nil, fmt.Errorf("the edited field map does not parse: %w", err)
	}
	return out.Bytes(), nil
}

// Parse decodes a field-map file's content (LoadFile's parser, for content already
// read). Empty content is a nil map.
func Parse(raw []byte) (*Map, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	var m Map
	if err := yaml.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("parsing field map: %w", err)
	}
	if m.Version == 0 {
		m.Version = 1
	}
	return &m, nil
}

func scalar(v string) *yaml.Node { return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v} }

// mappingValue returns the value node of key in a mapping node, or nil.
func mappingValue(m *yaml.Node, key string) *yaml.Node {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}
