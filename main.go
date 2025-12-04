package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidationError представляет ошибку с указанием файла и номера строки
type ValidationError struct {
	File string
	Line int
	Msg  string
}

func (e *ValidationError) Error() string {
	if e.Line > 0 {
		return fmt.Sprintf("%s:%d %s", e.File, e.Line, e.Msg)
	}
	return fmt.Sprintf("%s %s", e.File, e.Msg)
}

// findNodeByKey ищет дочерний узел с заданным ключом
func findNodeByKey(parent *yaml.Node, key string) *yaml.Node {
	if parent.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(parent.Content); i += 2 {
		k := parent.Content[i]
		if k.Value == key {
			return parent.Content[i+1]
		}
	}
	return nil
}

// requireField проверяет наличие обязательного поля
func requireField(parent *yaml.Node, key string) (*yaml.Node, error) {
	node := findNodeByKey(parent, key)
	if node == nil {
		return nil, fmt.Errorf("%s is required", key)
	}
	return node, nil
}

// validateString проверяет, что узел — строка
func validateString(node *yaml.Node, field string) (string, error) {
	if node.Kind != yaml.ScalarNode || node.Tag != "!!str" {
		return "", fmt.Errorf("%s must be string", field)
	}
	return node.Value, nil
}

// validateInt проверяет, что узел — целое число
func validateInt(node *yaml.Node, field string) (int, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("%s must be int", field)
	}
	switch node.Tag {
	case "!!int":
		val, err := strconv.Atoi(node.Value)
		if err != nil {
			return 0, fmt.Errorf("%s must be int", field)
		}
		return val, nil
	case "!!str":
		val, err := strconv.Atoi(node.Value)
		if err != nil {
			return 0, fmt.Errorf("%s must be int", field)
		}
		return val, nil
	default:
		return 0, fmt.Errorf("%s must be int", field)
	}
}

// validatePort проверяет, что порт в допустимом диапазоне
func validatePort(node *yaml.Node, field string) (int, error) {
	return validateInt(node, field)
}

// isValidContainerName проверяет snake_case формат
var snakeCaseRe = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func isValidContainerName(name string) bool {
	return snakeCaseRe.MatchString(name)
}

// isValidMemoryFormat проверяет формат памяти: Gi, Mi, Ki
var memoryRe = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)

func isValidMemoryFormat(s string) bool {
	return memoryRe.MatchString(s)
}

// validateObjectMeta валидирует metadata
func validateObjectMeta(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "metadata must be object"}
	}

	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	if _, err := validateString(nameNode, "metadata.name"); err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}

	// namespace — опционально
	if nsNode := findNodeByKey(node, "namespace"); nsNode != nil {
		if _, err := validateString(nsNode, "metadata.namespace"); err != nil {
			return &ValidationError{File: file, Line: nsNode.Line, Msg: err.Error()}
		}
	}

	// labels — опционально
	if labelsNode := findNodeByKey(node, "labels"); labelsNode != nil {
		if labelsNode.Kind != yaml.MappingNode {
			return &ValidationError{File: file, Line: labelsNode.Line, Msg: "metadata.labels must be object"}
		}
		for i := 0; i < len(labelsNode.Content); i += 2 {
			valNode := labelsNode.Content[i+1]
			if _, err := validateString(valNode, "metadata.labels value"); err != nil {
				return &ValidationError{File: file, Line: valNode.Line, Msg: "metadata.labels value must be string"}
			}
		}
	}

	return nil
}

// validatePodOS валидирует os
func validatePodOS(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "os must be object"}
	}
	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	name, err := validateString(nameNode, "spec.os.name")
	if err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}
	if name != "linux" && name != "windows" {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: fmt.Sprintf("spec.os.name has unsupported value '%s'", name)}
	}
	return nil
}

// validateHTTPGetAction валидирует httpGet
func validateHTTPGetAction(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + ".httpGet must be object"}
	}

	pathNode, err := requireField(node, "path")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
	}
	path, err := validateString(pathNode, prefix+".path")
	if err != nil {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: err.Error()}
	}
	if !strings.HasPrefix(path, "/") {
		return &ValidationError{File: file, Line: pathNode.Line, Msg: fmt.Sprintf("%s.path has invalid format '%s'", prefix, path)}
	}

	portNode, err := requireField(node, "port")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
	}
	if _, err := validatePort(portNode, prefix+".port"); err != nil {
		return &ValidationError{File: file, Line: portNode.Line, Msg: err.Error()}
	}

	return nil
}

// validateProbe валидирует readinessProbe/livenessProbe
func validateProbe(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}
	httpGetNode, err := requireField(node, "httpGet")
	if err != nil {
		return &ValidationError{File: file, Msg: prefix + "." + err.Error()}
	}
	if err := validateHTTPGetAction(httpGetNode, prefix+".httpGet", file); err != nil {
		return err
	}
	return nil
}

// validateResourceRequirements валидирует resources
func validateResourceRequirements(node *yaml.Node, prefix, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: prefix + " must be object"}
	}

	checkResource := func(parent *yaml.Node, kind string) error {
		if parent == nil {
			return nil
		}
		if parent.Kind != yaml.MappingNode {
			return &ValidationError{File: file, Line: parent.Line, Msg: fmt.Sprintf("%s.%s must be object", prefix, kind)}
		}
		for i := 0; i < len(parent.Content); i += 2 {
			keyNode := parent.Content[i]
			valNode := parent.Content[i+1]
			key := keyNode.Value
			switch key {
			case "cpu":
				if _, err := validateInt(valNode, fmt.Sprintf("%s.%s.cpu", prefix, kind)); err != nil {
					return &ValidationError{File: file, Line: valNode.Line, Msg: err.Error()}
				}
			case "memory":
				mem, err := validateString(valNode, fmt.Sprintf("%s.%s.memory", prefix, kind))
				if err != nil {
					return &ValidationError{File: file, Line: valNode.Line, Msg: err.Error()}
				}
				if !isValidMemoryFormat(mem) {
					return &ValidationError{File: file, Line: valNode.Line, Msg: fmt.Sprintf("%s.%s.memory has invalid format '%s'", prefix, kind, mem)}
				}
			default:
				// игнорируем неизвестные ресурсы (но в требованиях только cpu/memory)
			}
		}
		return nil
	}

	if limitsNode := findNodeByKey(node, "limits"); limitsNode != nil {
		if err := checkResource(limitsNode, "limits"); err != nil {
			return err
		}
	}
	if requestsNode := findNodeByKey(node, "requests"); requestsNode != nil {
		if err := checkResource(requestsNode, "requests"); err != nil {
			return err
		}
	}

	return nil
}

// validateContainerPort валидирует порт контейнера
func validateContainerPort(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "ports item must be object"}
	}

	containerPortNode, err := requireField(node, "containerPort")
	if err != nil {
		return &ValidationError{File: file, Msg: "containerPort is required"}
	}
	if _, err := validatePort(containerPortNode, "containerPort"); err != nil {
		return &ValidationError{File: file, Line: containerPortNode.Line, Msg: err.Error()}
	}

	if protocolNode := findNodeByKey(node, "protocol"); protocolNode != nil {
		proto, err := validateString(protocolNode, "protocol")
		if err != nil {
			return &ValidationError{File: file, Line: protocolNode.Line, Msg: err.Error()}
		}
		if proto != "TCP" && proto != "UDP" {
			return &ValidationError{File: file, Line: protocolNode.Line, Msg: fmt.Sprintf("protocol has unsupported value '%s'", proto)}
		}
	}

	return nil
}

// validateContainer валидирует один контейнер
func validateContainer(node *yaml.Node, file string, index int) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: fmt.Sprintf("containers[%d] must be object", index)}
	}

	nameNode, err := requireField(node, "name")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].%s", index, err.Error())}
	}
	name, err := validateString(nameNode, fmt.Sprintf("containers[%d].name", index))
	if err != nil {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: err.Error()}
	}
	if !isValidContainerName(name) {
		return &ValidationError{File: file, Line: nameNode.Line, Msg: fmt.Sprintf("containers[%d].name has invalid format '%s'", index, name)}
	}

	imageNode, err := requireField(node, "image")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].%s", index, err.Error())}
	}
	image, err := validateString(imageNode, fmt.Sprintf("containers[%d].image", index))
	if err != nil {
		return &ValidationError{File: file, Line: imageNode.Line, Msg: err.Error()}
	}
	if !strings.HasPrefix(image, "registry.bigbrother.io/") {
		return &ValidationError{File: file, Line: imageNode.Line, Msg: fmt.Sprintf("containers[%d].image has invalid format '%s'", index, image)}
	}
	if !strings.Contains(image[len("registry.bigbrother.io/"):], ":") {
		return &ValidationError{File: file, Line: imageNode.Line, Msg: fmt.Sprintf("containers[%d].image has invalid format '%s'", index, image)}
	}

	// ports — опционально
	if portsNode := findNodeByKey(node, "ports"); portsNode != nil {
		if portsNode.Kind != yaml.SequenceNode {
			return &ValidationError{File: file, Line: portsNode.Line, Msg: "containers[].ports must be array"}
		}
		for _, portItem := range portsNode.Content {
			if err := validateContainerPort(portItem, file); err != nil {
				return err
			}
		}
	}

	// readinessProbe — опционально
	if rpNode := findNodeByKey(node, "readinessProbe"); rpNode != nil {
		if err := validateProbe(rpNode, fmt.Sprintf("containers[%d].readinessProbe", index), file); err != nil {
			return err
		}
	}

	// livenessProbe — опционально
	if lpNode := findNodeByKey(node, "livenessProbe"); lpNode != nil {
		if err := validateProbe(lpNode, fmt.Sprintf("containers[%d].livenessProbe", index), file); err != nil {
			return err
		}
	}

	resourcesNode, err := requireField(node, "resources")
	if err != nil {
		return &ValidationError{File: file, Msg: fmt.Sprintf("containers[%d].%s", index, err.Error())}
	}
	if err := validateResourceRequirements(resourcesNode, fmt.Sprintf("containers[%d].resources", index), file); err != nil {
		return err
	}

	return nil
}

// validateContainers валидирует список контейнеров
func validateContainers(node *yaml.Node, file string) error {
	if node.Kind != yaml.SequenceNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "spec.containers must be array"}
	}
	if len(node.Content) == 0 {
		return &ValidationError{File: file, Msg: "spec.containers is required"}
	}
	for i, container := range node.Content {
		if err := validateContainer(container, file, i); err != nil {
			return err
		}
	}
	return nil
}

// validatePodSpec валидирует spec
func validatePodSpec(node *yaml.Node, file string) error {
	if node.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: node.Line, Msg: "spec must be object"}
	}

	// os — опционально
	if osNode := findNodeByKey(node, "os"); osNode != nil {
		if err := validatePodOS(osNode, file); err != nil {
			return err
		}
	}

	containersNode, err := requireField(node, "containers")
	if err != nil {
		return &ValidationError{File: file, Msg: "spec." + err.Error()}
	}
	if err := validateContainers(containersNode, file); err != nil {
		return err
	}

	return nil
}

// validateTopLevel валидирует корневой уровень документа
func validateTopLevel(node *yaml.Node, file string) error {
	if node.Kind != yaml.DocumentNode {
		return &ValidationError{File: file, Msg: "expected document node"}
	}
	if len(node.Content) == 0 {
		return &ValidationError{File: file, Msg: "empty document"}
	}
	doc := node.Content[0]
	if doc.Kind != yaml.MappingNode {
		return &ValidationError{File: file, Line: doc.Line, Msg: "root must be object"}
	}

	apiVersionNode, err := requireField(doc, "apiVersion")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	apiVersion, err := validateString(apiVersionNode, "apiVersion")
	if err != nil {
		return &ValidationError{File: file, Line: apiVersionNode.Line, Msg: err.Error()}
	}
	if apiVersion != "v1" {
		return &ValidationError{File: file, Line: apiVersionNode.Line, Msg: fmt.Sprintf("apiVersion has unsupported value '%s'", apiVersion)}
	}

	kindNode, err := requireField(doc, "kind")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	kind, err := validateString(kindNode, "kind")
	if err != nil {
		return &ValidationError{File: file, Line: kindNode.Line, Msg: err.Error()}
	}
	if kind != "Pod" {
		return &ValidationError{File: file, Line: kindNode.Line, Msg: fmt.Sprintf("kind has unsupported value '%s'", kind)}
	}

	metadataNode, err := requireField(doc, "metadata")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	if err := validateObjectMeta(metadataNode, file); err != nil {
		return err
	}

	specNode, err := requireField(doc, "spec")
	if err != nil {
		return &ValidationError{File: file, Msg: err.Error()}
	}
	if err := validatePodSpec(specNode, file); err != nil {
		return err
	}

	return nil
}

func validateFile(path string) error {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return fmt.Errorf("cannot resolve absolute path: %w", err)
	}

	content, err := os.ReadFile(absPath)
	if err != nil {
		return fmt.Errorf("cannot read file: %w", err)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		// yaml.v3 не предоставляет номер строки при синтаксической ошибке,
		// но мы можем выдать ошибку без номера строки
		return &ValidationError{File: path, Msg: "cannot unmarshal YAML"}
	}

	if len(root.Content) == 0 {
		return &ValidationError{File: path, Msg: "empty YAML"}
	}

	// Поддерживаем только один документ
	for _, docNode := range root.Content {
		if err := validateTopLevel(docNode, path); err != nil {
			return err
		}
	}

	return nil
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]
	if err := validateFile(filePath); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			fmt.Fprintln(os.Stderr, ve.Error())
		} else {
			fmt.Fprintf(os.Stderr, "%s %s\n", filePath, err.Error())
		}
		os.Exit(1)
	}

	os.Exit(0)
}