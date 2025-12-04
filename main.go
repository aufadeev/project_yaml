package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	domainRequired = "registry.bigbrother.io"
)

var (
	validOSNames      = map[string]bool{"linux": true, "windows": true}
	validProtocols    = map[string]bool{"TCP": true, "UDP": true}
	validResourceKeys = map[string]bool{"cpu": true, "memory": true}
	memoryUnitRegex   = regexp.MustCompile(`^(\d+)(Gi|Mi|Ki)$`)
	snakeCaseRegex    = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
)

type ValidationError struct {
	Filename string
	Line     int
	Message  string
}

func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d %s", e.Filename, e.Line, e.Message)
	}
	return fmt.Sprintf("%s %s", e.Filename, e.Message)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filename := os.Args[1]
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", filename, err)
		os.Exit(1)
	}

	if len(root.Content) == 0 {
		exitWithError(filename, 0, "empty YAML document")
	}

	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		exitWithError(filename, doc.Line, "root must be a mapping")
	}

	validator := &podValidator{
		filename: filename,
		content:  content,
	}
	err = validator.validatePod(doc)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	os.Exit(0)
}

type podValidator struct {
	filename string
	content  []byte
}

func (v *podValidator) validatePod(node *yaml.Node) error {
	fields := v.parseMapping(node)

	apiVersion, ok := fields["apiVersion"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "apiVersion is required"}
	}
	if apiVersion.Value != "v1" {
		return &ValidationError{Filename: v.filename, Line: apiVersion.Line, Message: "apiVersion has unsupported value '" + apiVersion.Value + "'"}
	}

	kind, ok := fields["kind"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "kind is required"}
	}
	if kind.Value != "Pod" {
		return &ValidationError{Filename: v.filename, Line: kind.Line, Message: "kind has unsupported value '" + kind.Value + "'"}
	}

	metadata, ok := fields["metadata"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "metadata is required"}
	}
	if err := v.validateObjectMeta(metadata); err != nil {
		return err
	}

	spec, ok := fields["spec"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "spec is required"}
	}
	if err := v.validatePodSpec(spec); err != nil {
		return err
	}

	return nil
}

func (v *podValidator) validateObjectMeta(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{Filename: v.filename, Line: node.Line, Message: "metadata must be a mapping"}
	}

	fields := v.parseMapping(node)

	name, ok := fields["name"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "metadata.name is required"}
	}
	if name.Kind != yaml.ScalarNode || name.Value == "" {
		return &ValidationError{Filename: v.filename, Line: name.Line, Message: "metadata.name must be string"}
	}

	if ns, ok := fields["namespace"]; ok {
		if ns.Kind != yaml.ScalarNode || ns.Value == "" {
			return &ValidationError{Filename: v.filename, Line: ns.Line, Message: "metadata.namespace must be string"}
		}
	}

	if labels, ok := fields["labels"]; ok {
		if labels.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: labels.Line, Message: "metadata.labels must be a mapping"}
		}
		for _, child := range labels.Content {
			if child.Kind != yaml.ScalarNode {
				return &ValidationError{Filename: v.filename, Line: child.Line, Message: "metadata.labels keys and values must be strings"}
			}
		}
	}

	return nil
}

func (v *podValidator) validatePodSpec(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{Filename: v.filename, Line: node.Line, Message: "spec must be a mapping"}
	}

	fields := v.parseMapping(node)

	if osNode, ok := fields["os"]; ok {
		if osNode.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: osNode.Line, Message: "spec.os must be a mapping"}
		}
		if err := v.validatePodOS(osNode); err != nil {
			return err
		}
	}

	containers, ok := fields["containers"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "spec.containers is required"}
	}
	if containers.Kind != yaml.SequenceNode {
		return &ValidationError{Filename: v.filename, Line: containers.Line, Message: "spec.containers must be a sequence"}
	}
	if len(containers.Content) == 0 {
		return &ValidationError{Filename: v.filename, Line: containers.Line, Message: "spec.containers must not be empty"}
	}

	seenNames := make(map[string]bool)
	for _, container := range containers.Content {
		if container.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: container.Line, Message: "container must be a mapping"}
		}
		if err := v.validateContainer(container, seenNames); err != nil {
			return err
		}
	}

	return nil
}

func (v *podValidator) validatePodOS(node *yaml.Node) error {
	fields := v.parseMapping(node)

	name, ok := fields["name"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "spec.os.name is required"}
	}
	if !validOSNames[name.Value] {
		return &ValidationError{Filename: v.filename, Line: name.Line, Message: "spec.os has unsupported value '" + name.Value + "'"}
	}
	return nil
}

func (v *podValidator) validateContainer(node *yaml.Node, seenNames map[string]bool) error {
	fields := v.parseMapping(node)

	nameNode, ok := fields["name"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "container.name is required"}
	}
	if nameNode.Kind != yaml.ScalarNode {
		return &ValidationError{Filename: v.filename, Line: nameNode.Line, Message: "container.name must be string"}
	}
	if !snakeCaseRegex.MatchString(nameNode.Value) {
		return &ValidationError{Filename: v.filename, Line: nameNode.Line, Message: "container.name has invalid format '" + nameNode.Value + "'"}
	}
	if seenNames[nameNode.Value] {
		return &ValidationError{Filename: v.filename, Line: nameNode.Line, Message: "container.name must be unique within pod"}
	}
	seenNames[nameNode.Value] = true

	imageNode, ok := fields["image"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "container.image is required"}
	}
	if imageNode.Kind != yaml.ScalarNode || imageNode.Value == "" {
		return &ValidationError{Filename: v.filename, Line: imageNode.Line, Message: "container.image must be string"}
	}
	if err := v.validateImage(imageNode.Value); err != nil {
		return &ValidationError{Filename: v.filename, Line: imageNode.Line, Message: "container.image " + err.Error()}
	}

	if portsNode, ok := fields["ports"]; ok {
		if portsNode.Kind != yaml.SequenceNode {
			return &ValidationError{Filename: v.filename, Line: portsNode.Line, Message: "container.ports must be a sequence"}
		}
		for _, port := range portsNode.Content {
			if err := v.validateContainerPort(port); err != nil {
				return err
			}
		}
	}

	if rp, ok := fields["readinessProbe"]; ok {
		if rp.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: rp.Line, Message: "readinessProbe must be a mapping"}
		}
		if err := v.validateProbe(rp, "readinessProbe"); err != nil {
			return err
		}
	}

	if lp, ok := fields["livenessProbe"]; ok {
		if lp.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: lp.Line, Message: "livenessProbe must be a mapping"}
		}
		if err := v.validateProbe(lp, "livenessProbe"); err != nil {
			return err
		}
	}

	resources, ok := fields["resources"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "container.resources is required"}
	}
	if resources.Kind != yaml.MappingNode {
		return &ValidationError{Filename: v.filename, Line: resources.Line, Message: "container.resources must be a mapping"}
	}
	if err := v.validateResourceRequirements(resources); err != nil {
		return err
	}

	return nil
}

func (v *podValidator) validateImage(image string) error {
	parts := strings.Split(image, "/")
	if len(parts) < 2 {
		return fmt.Errorf("has invalid format '%s'", image)
	}
	if parts[0] != domainRequired {
		return fmt.Errorf("must be in domain '%s'", domainRequired)
	}

	lastPart := parts[len(parts)-1]
	if !strings.Contains(lastPart, ":") {
		return fmt.Errorf("tag is required in '%s'", image)
	}
	tag := strings.Split(lastPart, ":")
	if len(tag) < 2 || tag[1] == "" {
		return fmt.Errorf("tag is required in '%s'", image)
	}

	return nil
}

func (v *podValidator) validateContainerPort(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{Filename: v.filename, Line: node.Line, Message: "containerPort must be a mapping"}
	}

	fields := v.parseMapping(node)

	containerPort, ok := fields["containerPort"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: "containerPort is required"}
	}
	port, err := v.parseInt(containerPort)
	if err != nil {
		return &ValidationError{Filename: v.filename, Line: containerPort.Line, Message: "containerPort must be int"}
	}
	if port <= 0 || port >= 65536 {
		return &ValidationError{Filename: v.filename, Line: containerPort.Line, Message: "containerPort value out of range"}
	}

	if proto, ok := fields["protocol"]; ok {
		if proto.Kind != yaml.ScalarNode {
			return &ValidationError{Filename: v.filename, Line: proto.Line, Message: "protocol must be string"}
		}
		if !validProtocols[proto.Value] {
			return &ValidationError{Filename: v.filename, Line: proto.Line, Message: "protocol has unsupported value '" + proto.Value + "'"}
		}
	}

	return nil
}

func (v *podValidator) validateProbe(node *yaml.Node, probeName string) error {
	fields := v.parseMapping(node)

	httpGet, ok := fields["httpGet"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: probeName + ".httpGet is required"}
	}
	if httpGet.Kind != yaml.MappingNode {
		return &ValidationError{Filename: v.filename, Line: httpGet.Line, Message: probeName + ".httpGet must be a mapping"}
	}

	httpFields := v.parseMapping(httpGet)

	path, ok := httpFields["path"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: probeName + ".httpGet.path is required"}
	}
	if path.Kind != yaml.ScalarNode || !strings.HasPrefix(path.Value, "/") {
		return &ValidationError{Filename: v.filename, Line: path.Line, Message: probeName + ".httpGet.path must be absolute path"}
	}

	portNode, ok := httpFields["port"]
	if !ok {
		return &ValidationError{Filename: v.filename, Message: probeName + ".httpGet.port is required"}
	}
	port, err := v.parseInt(portNode)
	if err != nil {
		return &ValidationError{Filename: v.filename, Line: portNode.Line, Message: probeName + ".httpGet.port must be int"}
	}
	if port <= 0 || port >= 65536 {
		return &ValidationError{Filename: v.filename, Line: portNode.Line, Message: probeName + ".httpGet.port value out of range"}
	}

	return nil
}

func (v *podValidator) validateResourceRequirements(node *yaml.Node) error {
	fields := v.parseMapping(node)

	if req, ok := fields["requests"]; ok {
		if req.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: req.Line, Message: "resources.requests must be a mapping"}
		}
		if err := v.validateResourceMap(req, "requests"); err != nil {
			return err
		}
	}

	if lim, ok := fields["limits"]; ok {
		if lim.Kind != yaml.MappingNode {
			return &ValidationError{Filename: v.filename, Line: lim.Line, Message: "resources.limits must be a mapping"}
		}
		if err := v.validateResourceMap(lim, "limits"); err != nil {
			return err
		}
	}

	return nil
}

func (v *podValidator) validateResourceMap(node *yaml.Node, section string) error {
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valueNode := node.Content[i+1]

		if keyNode.Kind != yaml.ScalarNode {
			return &ValidationError{Filename: v.filename, Line: keyNode.Line, Message: "resources." + section + " keys must be strings"}
		}

		key := keyNode.Value
		if !validResourceKeys[key] {
			return &ValidationError{Filename: v.filename, Line: keyNode.Line, Message: "resources." + section + " has unsupported resource '" + key + "'"}
		}

		switch key {
		case "cpu":
			if valueNode.Kind != yaml.ScalarNode {
				return &ValidationError{Filename: v.filename, Line: valueNode.Line, Message: "resources." + section + ".cpu must be int"}
			}
			if _, err := v.parseInt(valueNode); err != nil {
				return &ValidationError{Filename: v.filename, Line: valueNode.Line, Message: "resources." + section + ".cpu must be int"}
			}
		case "memory":
			if valueNode.Kind != yaml.ScalarNode {
				return &ValidationError{Filename: v.filename, Line: valueNode.Line, Message: "resources." + section + ".memory must be string"}
			}
			if !memoryUnitRegex.MatchString(valueNode.Value) {
				return &ValidationError{Filename: v.filename, Line: valueNode.Line, Message: "resources." + section + ".memory has invalid format '" + valueNode.Value + "'"}
			}
		}
	}
	return nil
}

func (v *podValidator) parseInt(node *yaml.Node) (int, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("not a scalar")
	}
	i, err := strconv.Atoi(node.Value)
	if err != nil {
		return 0, err
	}
	return i, nil
}

func (v *podValidator) parseMapping(node *yaml.Node) map[string]*yaml.Node {
	result := make(map[string]*yaml.Node)
	if node.Kind != yaml.MappingNode {
		return result
	}
	for i := 0; i < len(node.Content); i += 2 {
		key := node.Content[i]
		value := node.Content[i+1]
		if key.Kind == yaml.ScalarNode {
			result[key.Value] = value
		}
	}
	return result
}

func exitWithError(filename string, line int, msg string) {
	if line > 0 {
		fmt.Fprintf(os.Stderr, "%s:%d %s\n", filename, line, msg)
	} else {
		fmt.Fprintf(os.Stderr, "%s %s\n", filename, msg)
	}
	os.Exit(1)
}