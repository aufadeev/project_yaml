package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// ValidationError представляет ошибку валидации с указанием строки
type ValidationError struct {
	Line    int
	Message string
}

func (e *ValidationError) Error() string {
	return e.Message
}

// yamlNodeHelper упрощает поиск и обход YAML-узлов
type yamlNodeHelper struct {
	node *yaml.Node
}

func newHelper(node *yaml.Node) *yamlNodeHelper {
	return &yamlNodeHelper{node: node}
}

// getMapValueByKey ищет значение по ключу в мапе (представлена как последовательность пар ключ-значение)
func (h *yamlNodeHelper) getMapValueByKey(key string) *yaml.Node {
	if h.node.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i < len(h.node.Content); i += 2 {
		k := h.node.Content[i]
		if k.Value == key {
			return h.node.Content[i+1]
		}
	}
	return nil
}

// validate проверяет корректность Pod-файла
func validate(filePath string, content []byte) error {
	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		return fmt.Errorf("cannot unmarshal YAML: %w", err)
	}

	if len(root.Content) == 0 {
		return fmt.Errorf("empty YAML document")
	}

	// Ожидаем один документ
	doc := root.Content[0]
	if doc.Kind != yaml.MappingNode {
		return fmt.Errorf("root must be a mapping")
	}

	helper := newHelper(doc)

	// === 1. Поля верхнего уровня ===
	apiVersionNode := helper.getMapValueByKey("apiVersion")
	if apiVersionNode == nil {
		return fmt.Errorf("apiVersion is required")
	}
	if apiVersionNode.Value != "v1" {
		return &ValidationError{Line: apiVersionNode.Line, Message: "apiVersion has unsupported value '" + apiVersionNode.Value + "'"}
	}

	kindNode := helper.getMapValueByKey("kind")
	if kindNode == nil {
		return fmt.Errorf("kind is required")
	}
	if kindNode.Value != "Pod" {
		return &ValidationError{Line: kindNode.Line, Message: "kind has unsupported value '" + kindNode.Value + "'"}
	}

	metadataNode := helper.getMapValueByKey("metadata")
	if metadataNode == nil {
		return fmt.Errorf("metadata is required")
	}
	if err := validateObjectMeta(metadataNode); err != nil {
		return err
	}

	specNode := helper.getMapValueByKey("spec")
	if specNode == nil {
		return fmt.Errorf("spec is required")
	}
	if err := validatePodSpec(specNode); err != nil {
		return err
	}

	return nil
}

func validateObjectMeta(node *yaml.Node) error {
	h := newHelper(node)
	if node.Kind != yaml.MappingNode {
		return &ValidationError{Line: node.Line, Message: "metadata must be a mapping"}
	}

	nameNode := h.getMapValueByKey("name")
	if nameNode == nil {
		return fmt.Errorf("metadata.name is required")
	}
	if nameNode.Kind != yaml.ScalarNode || nameNode.Tag != "!!str" {
		return &ValidationError{Line: nameNode.Line, Message: "metadata.name must be string"}
	}

	// namespace — не обязателен
	namespaceNode := h.getMapValueByKey("namespace")
	if namespaceNode != nil && (namespaceNode.Kind != yaml.ScalarNode || namespaceNode.Tag != "!!str") {
		return &ValidationError{Line: namespaceNode.Line, Message: "metadata.namespace must be string"}
	}

	// labels — не обязателен
	labelsNode := h.getMapValueByKey("labels")
	if labelsNode != nil {
		if labelsNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: labelsNode.Line, Message: "metadata.labels must be a mapping"}
		}
		for i := 0; i < len(labelsNode.Content); i += 2 {
			val := labelsNode.Content[i+1]
			if val.Kind != yaml.ScalarNode || val.Tag != "!!str" {
				return &ValidationError{Line: val.Line, Message: "metadata.labels values must be strings"}
			}
		}
	}

	return nil
}

func validatePodSpec(node *yaml.Node) error {
	h := newHelper(node)
	if node.Kind != yaml.MappingNode {
		return &ValidationError{Line: node.Line, Message: "spec must be a mapping"}
	}

	// os — не обязателен
	osNode := h.getMapValueByKey("os")
	if osNode != nil {
		if osNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: osNode.Line, Message: "spec.os must be a mapping"}
		}
		osHelper := newHelper(osNode)
		nameNode := osHelper.getMapValueByKey("name")
		if nameNode == nil {
			return fmt.Errorf("spec.os.name is required")
		}
		if nameNode.Kind != yaml.ScalarNode || nameNode.Tag != "!!str" {
			return &ValidationError{Line: nameNode.Line, Message: "spec.os.name must be string"}
		}
		if nameNode.Value != "linux" && nameNode.Value != "windows" {
			return &ValidationError{Line: nameNode.Line, Message: "spec.os.name has unsupported value '" + nameNode.Value + "'"}
		}
	}

	// containers — обязателен
	containersNode := h.getMapValueByKey("containers")
	if containersNode == nil {
		return fmt.Errorf("spec.containers is required")
	}
	if containersNode.Kind != yaml.SequenceNode {
		return &ValidationError{Line: containersNode.Line, Message: "spec.containers must be a sequence"}
	}
	if len(containersNode.Content) == 0 {
		return &ValidationError{Line: containersNode.Line, Message: "spec.containers must not be empty"}
	}

	containerNames := make(map[string]bool)
	for _, containerNode := range containersNode.Content {
		if containerNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: containerNode.Line, Message: "container must be a mapping"}
		}
		if err := validateContainer(containerNode, containerNames); err != nil {
			return err
		}
	}

	return nil
}

func validateContainer(node *yaml.Node, seenNames map[string]bool) error {
	h := newHelper(node)

	nameNode := h.getMapValueByKey("name")
	if nameNode == nil {
		return fmt.Errorf("containers[].name is required")
	}
	if nameNode.Kind != yaml.ScalarNode || nameNode.Tag != "!!str" {
		return &ValidationError{Line: nameNode.Line, Message: "containers[].name must be string"}
	}
	if !isValidSnakeCase(nameNode.Value) {
		return &ValidationError{Line: nameNode.Line, Message: "containers[].name has invalid format '" + nameNode.Value + "'"}
	}
	if seenNames[nameNode.Value] {
		return &ValidationError{Line: nameNode.Line, Message: "containers[].name must be unique in pod"}
	}
	seenNames[nameNode.Value] = true

	imageNode := h.getMapValueByKey("image")
	if imageNode == nil {
		return fmt.Errorf("containers[].image is required")
	}
	if imageNode.Kind != yaml.ScalarNode || imageNode.Tag != "!!str" {
		return &ValidationError{Line: imageNode.Line, Message: "containers[].image must be string"}
	}
	if !isValidImage(imageNode.Value) {
		return &ValidationError{Line: imageNode.Line, Message: "containers[].image has invalid format '" + imageNode.Value + "'"}
	}

	// ports — не обязателен
	portsNode := h.getMapValueByKey("ports")
	if portsNode != nil {
		if portsNode.Kind != yaml.SequenceNode {
			return &ValidationError{Line: portsNode.Line, Message: "containers[].ports must be a sequence"}
		}
		for _, portNode := range portsNode.Content {
			if portNode.Kind != yaml.MappingNode {
				return &ValidationError{Line: portNode.Line, Message: "container port must be a mapping"}
			}
			if err := validateContainerPort(portNode); err != nil {
				return err
			}
		}
	}

	// readinessProbe — не обязателен
	readinessProbeNode := h.getMapValueByKey("readinessProbe")
	if readinessProbeNode != nil {
		if readinessProbeNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: readinessProbeNode.Line, Message: "containers[].readinessProbe must be a mapping"}
		}
		if err := validateProbe(readinessProbeNode); err != nil {
			return err
		}
	}

	// livenessProbe — не обязателен
	livenessProbeNode := h.getMapValueByKey("livenessProbe")
	if livenessProbeNode != nil {
		if livenessProbeNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: livenessProbeNode.Line, Message: "containers[].livenessProbe must be a mapping"}
		}
		if err := validateProbe(livenessProbeNode); err != nil {
			return err
		}
	}

	// resources — обязателен
	resourcesNode := h.getMapValueByKey("resources")
	if resourcesNode == nil {
		return fmt.Errorf("containers[].resources is required")
	}
	if resourcesNode.Kind != yaml.MappingNode {
		return &ValidationError{Line: resourcesNode.Line, Message: "containers[].resources must be a mapping"}
	}
	if err := validateResourceRequirements(resourcesNode); err != nil {
		return err
	}

	return nil
}

func validateContainerPort(node *yaml.Node) error {
	h := newHelper(node)

	containerPortNode := h.getMapValueByKey("containerPort")
	if containerPortNode == nil {
		return fmt.Errorf("containers[].ports[].containerPort is required")
	}
	port, err := parseInt(containerPortNode)
	if err != nil {
		return &ValidationError{Line: containerPortNode.Line, Message: "containers[].ports[].containerPort must be int"}
	}
	if port <= 0 || port >= 65536 {
		return &ValidationError{Line: containerPortNode.Line, Message: "containers[].ports[].containerPort value out of range"}
	}

	protocolNode := h.getMapValueByKey("protocol")
	if protocolNode != nil {
		if protocolNode.Kind != yaml.ScalarNode || protocolNode.Tag != "!!str" {
			return &ValidationError{Line: protocolNode.Line, Message: "containers[].ports[].protocol must be string"}
		}
		if protocolNode.Value != "TCP" && protocolNode.Value != "UDP" {
			return &ValidationError{Line: protocolNode.Line, Message: "containers[].ports[].protocol has unsupported value '" + protocolNode.Value + "'"}
		}
	}

	return nil
}

func validateProbe(node *yaml.Node) error {
	h := newHelper(node)

	httpGetNode := h.getMapValueByKey("httpGet")
	if httpGetNode == nil {
		return fmt.Errorf("probe.httpGet is required")
	}
	if httpGetNode.Kind != yaml.MappingNode {
		return &ValidationError{Line: httpGetNode.Line, Message: "probe.httpGet must be a mapping"}
	}

	pathNode := newHelper(httpGetNode).getMapValueByKey("path")
	if pathNode == nil {
		return fmt.Errorf("probe.httpGet.path is required")
	}
	if pathNode.Kind != yaml.ScalarNode || pathNode.Tag != "!!str" {
		return &ValidationError{Line: pathNode.Line, Message: "probe.httpGet.path must be string"}
	}
	if !strings.HasPrefix(pathNode.Value, "/") {
		return &ValidationError{Line: pathNode.Line, Message: "probe.httpGet.path has invalid format '" + pathNode.Value + "'"}
	}

	portNode := newHelper(httpGetNode).getMapValueByKey("port")
	if portNode == nil {
		return fmt.Errorf("probe.httpGet.port is required")
	}
	port, err := parseInt(portNode)
	if err != nil {
		return &ValidationError{Line: portNode.Line, Message: "probe.httpGet.port must be int"}
	}
	if port <= 0 || port >= 65536 {
		return &ValidationError{Line: portNode.Line, Message: "probe.httpGet.port value out of range"}
	}

	return nil
}

func validateResourceRequirements(node *yaml.Node) error {
	h := newHelper(node)

	// requests — не обязателен
	requestsNode := h.getMapValueByKey("requests")
	if requestsNode != nil {
		if requestsNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: requestsNode.Line, Message: "resources.requests must be a mapping"}
		}
		if err := validateResourceMap(requestsNode); err != nil {
			return err
		}
	}

	// limits — не обязателен
	limitsNode := h.getMapValueByKey("limits")
	if limitsNode != nil {
		if limitsNode.Kind != yaml.MappingNode {
			return &ValidationError{Line: limitsNode.Line, Message: "resources.limits must be a mapping"}
		}
		if err := validateResourceMap(limitsNode); err != nil {
			return err
		}
	}

	return nil
}

func validateResourceMap(node *yaml.Node) error {
	h := newHelper(node)
	for i := 0; i < len(node.Content); i += 2 {
		keyNode := node.Content[i]
		valNode := node.Content[i+1]

		switch keyNode.Value {
		case "cpu":
			if valNode.Kind != yaml.ScalarNode {
				return &ValidationError{Line: valNode.Line, Message: "resources.*.cpu must be integer"}
			}
			if _, err := strconv.Atoi(valNode.Value); err != nil {
				return &ValidationError{Line: valNode.Line, Message: "resources.*.cpu must be integer"}
			}
		case "memory":
			if valNode.Kind != yaml.ScalarNode || valNode.Tag != "!!str" {
				return &ValidationError{Line: valNode.Line, Message: "resources.*.memory must be string"}
			}
			if !isValidMemory(valNode.Value) {
				return &ValidationError{Line: valNode.Line, Message: "resources.*.memory has invalid format '" + valNode.Value + "'"}
			}
		default:
			// игнорируем неизвестные ресурсы, т.к. требования — только cpu/memory
		}
	}
	return nil
}

// Вспомогательные функции

var snakeCaseRegexp = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)
var memoryRegexp = regexp.MustCompile(`^\d+(Gi|Mi|Ki)$`)

func isValidSnakeCase(s string) bool {
	return snakeCaseRegexp.MatchString(s)
}

func isValidImage(s string) bool {
	// Должен начинаться с registry.bigbrother.io/
	if !strings.HasPrefix(s, "registry.bigbrother.io/") {
		return false
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 {
		return false
	}
	imageWithTag := strings.Join(parts[1:], "/")
	// Должен содержать тег
	if !strings.Contains(imageWithTag, ":") {
		return false
	}
	tagIndex := strings.LastIndex(imageWithTag, ":")
	if tagIndex == len(imageWithTag)-1 {
		return false // пустой тег
	}
	tag := imageWithTag[tagIndex+1:]
	if tag == "" {
		return false
	}
	// Дополнительно можно проверить, что тег не содержит запрещённых символов, но требования — просто наличие
	return true
}

func isValidMemory(s string) bool {
	return memoryRegexp.MatchString(s)
}

func parseInt(node *yaml.Node) (int, error) {
	if node.Kind != yaml.ScalarNode {
		return 0, fmt.Errorf("not scalar")
	}
	if node.Tag == "!!int" {
		return strconv.Atoi(node.Value)
	}
	if node.Tag == "!!str" {
		// YAML может хранить числа как строки
		return strconv.Atoi(node.Value)
	}
	return 0, fmt.Errorf("not an integer")
}

// === main ===

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintf(os.Stderr, "Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filePath := os.Args[1]
	content, err := os.ReadFile(filePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: cannot read file: %v\n", filePath, err)
		os.Exit(1)
	}

	if err := validate(filePath, content); err != nil {
		if ve, ok := err.(*ValidationError); ok {
			fmt.Fprintf(os.Stderr, "%s:%d %s\n", filePath, ve.Line, ve.Message)
		} else {
			// Даже глобальные ошибки — с именем файла!
			fmt.Fprintf(os.Stderr, "%s: %s\n", filePath, err.Error())
		}
		os.Exit(1)
	}
	// успех — ничего не выводим
}