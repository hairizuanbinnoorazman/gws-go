package cmd

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/hairizuanbinnoorazman/gws-go/internal/api"
	"github.com/hairizuanbinnoorazman/gws-go/internal/discovery"
	"github.com/spf13/cobra"
)

type discoveredParameterBinding struct {
	apiName    string
	flagName   string
	definition *discovery.Parameter
	single     string
	repeated   []string
}

func addDiscoveredParameterFlags(command *cobra.Command, doc *discovery.Document, method *discovery.Method, opts *api.Options) func() error {
	combined := make(map[string]*discovery.Parameter, len(doc.Parameters)+len(method.Parameters))
	for name, definition := range doc.Parameters {
		combined[name] = definition
	}
	for name, definition := range method.Parameters {
		combined[name] = definition
	}

	var bindings []*discoveredParameterBinding
	for _, apiName := range sortedParameterNames(combined) {
		definition := combined[apiName]
		flagName := parameterFlagName(apiName)
		if flagName == "help" || command.Flags().Lookup(flagName) != nil {
			flagName = "api-" + flagName
		}
		binding := &discoveredParameterBinding{
			apiName: apiName, flagName: flagName, definition: definition,
		}
		usage := discoveredParameterUsage(apiName, flagName, definition)
		if definition.Repeated {
			command.Flags().StringArrayVar(&binding.repeated, flagName, nil, usage)
		} else {
			command.Flags().StringVar(&binding.single, flagName, "", usage)
			if definition.Type == "boolean" {
				command.Flags().Lookup(flagName).NoOptDefVal = "true"
			}
		}
		bindings = append(bindings, binding)
	}

	return func() error {
		overrides := make(map[string]any)
		for _, binding := range bindings {
			if !command.Flags().Changed(binding.flagName) {
				continue
			}
			if binding.definition.Repeated {
				values := make([]any, 0, len(binding.repeated))
				for _, raw := range binding.repeated {
					value, err := parseDiscoveredParameterValue(raw, binding.definition)
					if err != nil {
						return fmt.Errorf("--%s: %w", binding.flagName, err)
					}
					values = append(values, value)
				}
				overrides[binding.apiName] = values
				continue
			}
			value, err := parseDiscoveredParameterValue(binding.single, binding.definition)
			if err != nil {
				return fmt.Errorf("--%s: %w", binding.flagName, err)
			}
			overrides[binding.apiName] = value
		}
		opts.ParameterOverrides = overrides
		return nil
	}
}

func sortedParameterNames(parameters map[string]*discovery.Parameter) []string {
	names := make([]string, 0, len(parameters))
	for name := range parameters {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func parameterFlagName(name string) string {
	var builder strings.Builder
	previousWasSeparator := true
	var previous rune
	for _, current := range name {
		if !unicode.IsLetter(current) && !unicode.IsDigit(current) {
			if !previousWasSeparator && builder.Len() > 0 {
				builder.WriteByte('-')
			}
			previousWasSeparator = true
			previous = current
			continue
		}
		if unicode.IsUpper(current) && builder.Len() > 0 && !previousWasSeparator &&
			(unicode.IsLower(previous) || unicode.IsDigit(previous)) {
			builder.WriteByte('-')
		}
		builder.WriteRune(unicode.ToLower(current))
		previousWasSeparator = false
		previous = current
	}
	result := strings.Trim(builder.String(), "-")
	if result == "" {
		return "parameter"
	}
	return result
}

func discoveredParameterUsage(apiName, flagName string, definition *discovery.Parameter) string {
	parts := []string{}
	if definition.Description != "" {
		parts = append(parts, firstLine(definition.Description))
	} else {
		parts = append(parts, "Google API parameter "+apiName)
	}
	if flagName != parameterFlagName(apiName) {
		parts = append(parts, "API name: "+apiName)
	}
	if definition.Required {
		parts = append(parts, "required")
	}
	if len(definition.Enum) > 0 {
		parts = append(parts, "values: "+strings.Join(definition.Enum, ", "))
	}
	return strings.Join(parts, "; ")
}

func parseDiscoveredParameterValue(raw string, definition *discovery.Parameter) (any, error) {
	if len(definition.Enum) > 0 {
		found := false
		for _, allowed := range definition.Enum {
			if raw == allowed {
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("must be one of %s", strings.Join(definition.Enum, ", "))
		}
	}
	switch definition.Type {
	case "boolean":
		value, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, fmt.Errorf("must be a boolean")
		}
		return value, nil
	case "integer":
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("must be an integer")
		}
		return value, nil
	case "number":
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return nil, fmt.Errorf("must be a number")
		}
		return value, nil
	default:
		return raw, nil
	}
}
