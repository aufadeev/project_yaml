package main

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type Validator struct {
	filename string
	errors   []string
}

func (v *Validator) errorf(line int, format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	baseFilename := strings.TrimPrefix(v.filename, "/tmp/")
	if line > 0 {
		errorMsg := fmt.Sprintf("%s:%d %s", baseFilename, line, msg)
		v.errors = append(v.errors, errorMsg)
	} else {
		errorMsg := fmt.Sprintf("%s %s", baseFilename, msg)
		v.errors = append(v.errors, errorMsg)
	}
}

func (v *Validator) validateTopLevel(doc *yaml.Node) {
	requiredFields := []string{"apiVersion", "kind", "metadata", "spec"}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(doc.Content); i += 2 {
		if i+1 < len(doc.Content) {
			key := doc.Content[i]
			value := doc.Content[i+1]
			fields[key.Value] = value
		}
	}

	for _, field := range requiredFields {
		if node, exists := fields[field]; !exists {
			v.errorf(doc.Line, "%s is required", field)
		} else {
			switch field {
			case "apiVersion":
				v.validateString(node, "apiVersion", []string{"v1"})
			case "kind":
				v.validateString(node, "kind", []string{"Pod"})
			case "metadata":
				v.validateMetadata(node)
			case "spec":
				v.validateSpec(node)
			}
		}
	}
}

func (v *Validator) validateMetadata(metadata *yaml.Node) {
	if metadata.Kind != yaml.MappingNode {
		v.errorf(metadata.Line, "metadata must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(metadata.Content); i += 2 {
		if i+1 < len(metadata.Content) {
			key := metadata.Content[i]
			value := metadata.Content[i+1]
			fields[key.Value] = value
		}
	}

	if name, exists := fields["name"]; !exists {
		v.errorf(metadata.Line, "name is required")
	} else {
		v.validateRequiredString(name, "name")
	}

	if namespace, exists := fields["namespace"]; exists {
		v.validateString(namespace, "namespace", nil)
	}

	if labels, exists := fields["labels"]; exists {
		if labels.Kind != yaml.MappingNode {
			v.errorf(labels.Line, "labels must be object")
		}
	}
}

func (v *Validator) validateSpec(spec *yaml.Node) {
	if spec.Kind != yaml.MappingNode {
		v.errorf(spec.Line, "spec must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(spec.Content); i += 2 {
		if i+1 < len(spec.Content) {
			key := spec.Content[i]
			value := spec.Content[i+1]
			fields[key.Value] = value
		}
	}

	if containers, exists := fields["containers"]; !exists {
		v.errorf(spec.Line, "containers is required")
	} else {
		v.validateContainers(containers)
	}

	if os, exists := fields["os"]; exists {
		v.validatePodOS(os)
	}
}

func (v *Validator) validatePodOS(podOS *yaml.Node) {
	if podOS.Kind == yaml.ScalarNode {
		// Обработка когда os указан как строка
		v.validateString(podOS, "os", []string{"linux", "windows"})
		return
	}

	if podOS.Kind != yaml.MappingNode {
		v.errorf(podOS.Line, "os must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(podOS.Content); i += 2 {
		if i+1 < len(podOS.Content) {
			key := podOS.Content[i]
			value := podOS.Content[i+1]
			fields[key.Value] = value
		}
	}

	if name, exists := fields["name"]; !exists {
		v.errorf(podOS.Line, "os.name is required")
	} else {
		v.validateString(name, "os.name", []string{"linux", "windows"})
	}
}

func (v *Validator) validateContainers(containers *yaml.Node) {
	if containers.Kind != yaml.SequenceNode {
		v.errorf(containers.Line, "containers must be array")
		return
	}

	containerNames := make(map[string]bool)
	for _, container := range containers.Content {
		v.validateContainer(container, containerNames)
	}
}

func (v *Validator) validateContainer(container *yaml.Node, containerNames map[string]bool) {
	if container.Kind != yaml.MappingNode {
		v.errorf(container.Line, "container must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(container.Content); i += 2 {
		if i+1 < len(container.Content) {
			key := container.Content[i]
			value := container.Content[i+1]
			fields[key.Value] = value
		}
	}

	// Проверяем обязательные поля
	if name, exists := fields["name"]; !exists {
		v.errorf(container.Line, "name is required")
	} else {
		v.validateContainerName(name, containerNames)
	}

	if image, exists := fields["image"]; !exists {
		v.errorf(container.Line, "image is required")
	} else {
		v.validateImage(image)
	}

	if resources, exists := fields["resources"]; !exists {
		v.errorf(container.Line, "resources is required")
	} else {
		v.validateResources(resources)
	}

	if ports, exists := fields["ports"]; exists {
		v.validatePorts(ports)
	}

	if readinessProbe, exists := fields["readinessProbe"]; exists {
		v.validateProbe(readinessProbe)
	}
	if livenessProbe, exists := fields["livenessProbe"]; exists {
		v.validateProbe(livenessProbe)
	}
}

func (v *Validator) validateContainerName(name *yaml.Node, containerNames map[string]bool) {
	if name.Kind != yaml.ScalarNode {
		v.errorf(name.Line, "name must be string")
		return
	}

	// Проверка на пустую строку
	if strings.TrimSpace(name.Value) == "" {
		v.errorf(name.Line, "name is required")
		return
	}

	snakeCaseRegex := regexp.MustCompile(`^[a-z]+(_[a-z]+)*$`)
	if !snakeCaseRegex.MatchString(name.Value) {
		v.errorf(name.Line, "name has invalid format '%s'", name.Value)
		return
	}

	if containerNames[name.Value] {
		v.errorf(name.Line, "name '%s' is not unique", name.Value)
	} else {
		containerNames[name.Value] = true
	}
}

func (v *Validator) validateImage(image *yaml.Node) {
	if image.Kind != yaml.ScalarNode {
		v.errorf(image.Line, "image must be string")
		return
	}

	imageRegex := regexp.MustCompile(`^registry\.bigbrother\.io/[^:]+:.+$`)
	if !imageRegex.MatchString(image.Value) {
		v.errorf(image.Line, "image has invalid format '%s'", image.Value)
	}
}

func (v *Validator) validatePorts(ports *yaml.Node) {
	if ports.Kind != yaml.SequenceNode {
		v.errorf(ports.Line, "ports must be array")
		return
	}

	for _, port := range ports.Content {
		v.validateContainerPort(port)
	}
}

func (v *Validator) validateContainerPort(port *yaml.Node) {
	if port.Kind != yaml.MappingNode {
		v.errorf(port.Line, "port must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(port.Content); i += 2 {
		if i+1 < len(port.Content) {
			key := port.Content[i]
			value := port.Content[i+1]
			fields[key.Value] = value
		}
	}

	if containerPort, exists := fields["containerPort"]; !exists {
		v.errorf(port.Line, "containerPort is required")
	} else {
		v.validatePortNumber(containerPort, "containerPort")
	}

	if protocol, exists := fields["protocol"]; exists {
		v.validateString(protocol, "protocol", []string{"TCP", "UDP"})
	}
}

func (v *Validator) validateProbe(probe *yaml.Node) {
	if probe.Kind != yaml.MappingNode {
		v.errorf(probe.Line, "probe must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(probe.Content); i += 2 {
		if i+1 < len(probe.Content) {
			key := probe.Content[i]
			value := probe.Content[i+1]
			fields[key.Value] = value
		}
	}

	if httpGet, exists := fields["httpGet"]; !exists {
		v.errorf(probe.Line, "httpGet is required")
	} else {
		v.validateHTTPGetAction(httpGet)
	}
}

func (v *Validator) validateHTTPGetAction(httpGet *yaml.Node) {
	if httpGet.Kind != yaml.MappingNode {
		v.errorf(httpGet.Line, "httpGet must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(httpGet.Content); i += 2 {
		if i+1 < len(httpGet.Content) {
			key := httpGet.Content[i]
			value := httpGet.Content[i+1]
			fields[key.Value] = value
		}
	}

	if path, exists := fields["path"]; !exists {
		v.errorf(httpGet.Line, "path is required")
	} else {
		v.validateAbsolutePath(path, "path")
	}

	if port, exists := fields["port"]; !exists {
		v.errorf(httpGet.Line, "port is required")
	} else {
		v.validatePortNumber(port, "port")
	}
}

func (v *Validator) validateResources(resources *yaml.Node) {
	if resources.Kind != yaml.MappingNode {
		v.errorf(resources.Line, "resources must be object")
		return
	}

	fields := make(map[string]*yaml.Node)
	for i := 0; i < len(resources.Content); i += 2 {
		if i+1 < len(resources.Content) {
			key := resources.Content[i]
			value := resources.Content[i+1]
			fields[key.Value] = value
		}
	}

	if requests, exists := fields["requests"]; exists {
		v.validateResourceRequirements(requests)
	}

	if limits, exists := fields["limits"]; exists {
		v.validateResourceRequirements(limits)
	}
}

func (v *Validator) validateResourceRequirements(resources *yaml.Node) {
	if resources.Kind != yaml.MappingNode {
		v.errorf(resources.Line, "resources must be object")
		return
	}

	for i := 0; i < len(resources.Content); i += 2 {
		if i+1 < len(resources.Content) {
			key := resources.Content[i]
			value := resources.Content[i+1]

			switch key.Value {
			case "cpu":
				v.validateCPU(value)
			case "memory":
				v.validateMemory(value)
			default:
				v.errorf(key.Line, "resources has unsupported resource '%s'", key.Value)
			}
		}
	}
}

func (v *Validator) validateCPU(cpu *yaml.Node) {
	if cpu.Kind != yaml.ScalarNode {
		v.errorf(cpu.Line, "cpu must be int")
		return
	}

	// Ключевое исправление: проверяем стиль YAML
	// Если значение было в кавычках, Tag будет "!!str" вместо "!!int"
	if cpu.Tag == "!!str" {
		// Это строка - проверяем, можно ли преобразовать в число
		if _, err := strconv.Atoi(cpu.Value); err != nil {
			v.errorf(cpu.Line, "cpu must be int")
		} else {
			// Можно преобразовать в число, но это все равно строка - ошибка
			v.errorf(cpu.Line, "cpu must be int")
		}
		return
	}

	// Если это не строка, проверяем что это число
	if cpu.Tag != "!!int" {
		v.errorf(cpu.Line, "cpu must be int")
		return
	}

	// Проверяем, что значение является числом
	if _, err := strconv.Atoi(cpu.Value); err != nil {
		v.errorf(cpu.Line, "cpu must be int")
	}
}

func (v *Validator) validateMemory(memory *yaml.Node) {
	if memory.Kind != yaml.ScalarNode {
		v.errorf(memory.Line, "memory must be string")
		return
	}

	// Убираем кавычки если они есть
	cleanedValue := strings.Trim(memory.Value, `"`)
	memoryRegex := regexp.MustCompile(`^[0-9]+(Gi|Mi|Ki)$`)
	if !memoryRegex.MatchString(cleanedValue) {
		v.errorf(memory.Line, "memory has invalid format '%s'", cleanedValue)
	}
}

func (v *Validator) validateRequiredString(node *yaml.Node, fieldPath string) {
	if node.Kind != yaml.ScalarNode {
		v.errorf(node.Line, "%s must be string", fieldPath)
		return
	}

	// Проверка на пустую строку
	if strings.TrimSpace(node.Value) == "" {
		v.errorf(node.Line, "%s is required", fieldPath)
	}
}

func (v *Validator) validateString(node *yaml.Node, fieldPath string, allowedValues []string) {
	if node.Kind != yaml.ScalarNode {
		v.errorf(node.Line, "%s must be string", fieldPath)
		return
	}

	if allowedValues != nil {
		found := false
		for _, allowed := range allowedValues {
			if node.Value == allowed {
				found = true
				break
			}
		}
		if !found {
			v.errorf(node.Line, "%s has unsupported value '%s'", fieldPath, node.Value)
		}
	}
}

func (v *Validator) validatePortNumber(port *yaml.Node, fieldPath string) {
	if port.Kind != yaml.ScalarNode {
		v.errorf(port.Line, "%s must be integer", fieldPath)
		return
	}

	portNum, err := strconv.Atoi(port.Value)
	if err != nil {
		v.errorf(port.Line, "%s must be integer", fieldPath)
		return
	}

	if portNum <= 0 || portNum >= 65536 {
		v.errorf(port.Line, "%s value out of range", fieldPath)
	}
}

func (v *Validator) validateAbsolutePath(path *yaml.Node, fieldPath string) {
	if path.Kind != yaml.ScalarNode {
		v.errorf(path.Line, "%s must be string", fieldPath)
		return
	}

	if !strings.HasPrefix(path.Value, "/") {
		v.errorf(path.Line, "%s must be absolute path", fieldPath)
	}
}

func main() {
	if len(os.Args) != 2 {
		fmt.Printf("Usage: %s <yaml-file>\n", os.Args[0])
		os.Exit(1)
	}

	filename := os.Args[1]
	content, err := os.ReadFile(filename)
	if err != nil {
		fmt.Printf("%s: cannot read file: %v\n", filename, err)
		os.Exit(1)
	}

	var root yaml.Node
	if err := yaml.Unmarshal(content, &root); err != nil {
		fmt.Printf("%s: cannot unmarshal YAML: %v\n", filename, err)
		os.Exit(1)
	}

	validator := &Validator{filename: filename}

	for _, doc := range root.Content {
		if doc.Kind == yaml.DocumentNode && len(doc.Content) > 0 {
			validator.validateTopLevel(doc.Content[0])
		} else {
			// Если это не DocumentNode, валидируем напрямую
			validator.validateTopLevel(doc)
		}
	}

	if len(validator.errors) > 0 {
		for _, err := range validator.errors {
			fmt.Println(err)
		}
		os.Exit(1)
	}

	os.Exit(0)
}