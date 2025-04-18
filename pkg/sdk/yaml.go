package sdk

import (
	"fmt"
	"io/ioutil"

	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
	"gopkg.in/yaml.v3"
)

// YAMLHelper is a utility class for YAML operations
type YAMLHelper struct{}

// NewYAMLHelper creates a new YAML helper
func NewYAMLHelper() *YAMLHelper {
	return &YAMLHelper{}
}

// ExportTypeToYAML exports a type definition to YAML
func (y *YAMLHelper) ExportTypeToYAML(typeDef *core.TypeDefinition) (string, error) {
	yamlDef, err := ExportTypeToYAML(typeDef)
	if err != nil {
		return "", err
	}

	data, err := yaml.Marshal(yamlDef)
	if err != nil {
		return "", err
	}

	return string(data), nil
}

// ImportTypeFromYAML imports a type definition from YAML string
func (y *YAMLHelper) ImportTypeFromYAML(yamlContent string) (*core.TypeDefinition, error) {
	var yamlDef TypeDefinitionYAML
	err := yaml.Unmarshal([]byte(yamlContent), &yamlDef)
	if err != nil {
		return nil, err
	}

	return ImportTypeFromYAML(&yamlDef)
}

// TypeDefinitionYAML represents a type definition in YAML format
type TypeDefinitionYAML struct {
	Name        string          `yaml:"name"`
	Description string          `yaml:"description"`
	Version     int             `yaml:"version"`
	ParentType  string          `yaml:"parentType,omitempty"`
	Attributes  []AttributeYAML `yaml:"attributes"`
}

// ValidationConfigYAML represents validation configuration in YAML format
type ValidationConfigYAML struct {
	Action       string        `yaml:"action,omitempty"`
	TargetField  string        `yaml:"targetField,omitempty"`
	Values       []interface{} `yaml:"values,omitempty"`
	StringValue  string        `yaml:"stringValue,omitempty"`
	NumericValue float64       `yaml:"numericValue,omitempty"`
}

// CascadeYAML represents cascade properties in YAML format
type CascadeYAML struct {
	ID               string                `yaml:"id,omitempty"`
	Enabled          bool                  `yaml:"enabled"`
	Behavior         string                `yaml:"behavior,omitempty"`
	Logic            string                `yaml:"logic,omitempty"`
	Weight           int                   `yaml:"weight,omitempty"`
	ValidationConfig *ValidationConfigYAML `yaml:"validationConfig,omitempty"`
}

// AttributeYAML represents an attribute definition in YAML format
type AttributeYAML struct {
	Name            string               `yaml:"name"`
	Description     string               `yaml:"description"`
	DataType        string               `yaml:"dataType"`
	Required        bool                 `yaml:"required"`
	DefaultValue    interface{}          `yaml:"defaultValue,omitempty"`
	MultiValued     bool                 `yaml:"multiValued,omitempty"`
	Disabled        bool                 `yaml:"disabled,omitempty"`
	Cascades        []CascadeYAML        `yaml:"cascades,omitempty"` // Multiple cascades for inheritance
	ValidationRules []ValidationRuleYAML `yaml:"validationRules,omitempty"`
}

// ValidationRuleYAML represents a validation rule in YAML format
type ValidationRuleYAML struct {
	Type       string                 `yaml:"type"`
	Parameters map[string]interface{} `yaml:"parameters,omitempty"`
}

// ExportTypeToYAML exports a type definition to YAML format
func ExportTypeToYAML(typeDef *core.TypeDefinition) (*TypeDefinitionYAML, error) {
	parentTypeID := ""
	if typeDef.ParentType != nil {
		parentTypeID = typeDef.ParentType.Name
	}

	yamlDef := &TypeDefinitionYAML{
		Name:        typeDef.Name,
		Description: typeDef.Description,
		Version:     typeDef.Version,
		ParentType:  parentTypeID,
		Attributes:  make([]AttributeYAML, 0, len(typeDef.Attributes)),
	}

	// Convert attributes
	for _, attr := range typeDef.Attributes {
		yamlAttr := AttributeYAML{
			Name:            attr.Name,
			Description:     attr.Description,
			DataType:        string(attr.DataType),
			Required:        attr.Required,
			DefaultValue:    attr.DefaultValue,
			MultiValued:     attr.MultiValued,
			Disabled:        attr.Disabled,
			Cascades:        make([]CascadeYAML, 0, len(attr.Cascades)),
			ValidationRules: make([]ValidationRuleYAML, 0, len(attr.ValidationRules)),
		}

		// Convert all cascades
		for _, cascade := range attr.Cascades {
			yamlCascade := CascadeYAML{
				ID:       cascade.ID,
				Enabled:  cascade.Enabled,
				Behavior: string(cascade.Behavior),
				Logic:    cascade.Logic,
				Weight:   cascade.Weight,
			}

			// Add validation configuration if present
			if cascade.ValidationConfig != nil {
				yamlValidationConfig := &ValidationConfigYAML{
					Action:       string(cascade.ValidationConfig.Action),
					TargetField:  cascade.ValidationConfig.TargetField,
					Values:       cascade.ValidationConfig.Values,
					StringValue:  cascade.ValidationConfig.StringValue,
					NumericValue: cascade.ValidationConfig.NumericValue,
				}
				yamlCascade.ValidationConfig = yamlValidationConfig
			}

			yamlAttr.Cascades = append(yamlAttr.Cascades, yamlCascade)
		}

		// Convert validation rules
		for _, rule := range attr.ValidationRules {
			yamlRule := ValidationRuleYAML{
				Parameters: make(map[string]interface{}),
			}

			// Export different rule types
			switch r := rule.(type) {
			case *validation.RequiredRule:
				yamlRule.Type = "required"

			case *validation.MinLengthRule:
				yamlRule.Type = "minLength"
				yamlRule.Parameters["minLength"] = r.MinLength

			case *validation.MaxLengthRule:
				yamlRule.Type = "maxLength"
				yamlRule.Parameters["maxLength"] = r.MaxLength

			case *validation.PatternRule:
				yamlRule.Type = "pattern"
				yamlRule.Parameters["pattern"] = r.Pattern

			case *validation.EnumRule:
				yamlRule.Type = "enum"
				yamlRule.Parameters["allowedValues"] = r.AllowedValues

			case *validation.RangeRule:
				yamlRule.Type = "range"
				if r.Min != nil {
					yamlRule.Parameters["min"] = *r.Min
				}
				if r.Max != nil {
					yamlRule.Parameters["max"] = *r.Max
				}

			case *validation.CustomRule:
				yamlRule.Type = "custom"
				yamlRule.Parameters["description"] = r.Description

			case *validation.GenericRule:
				yamlRule.Type = "generic"
				// Generic rule doesn't have any parameters

			default:
				return nil, fmt.Errorf("unsupported validation rule type: %T", rule)
			}

			yamlAttr.ValidationRules = append(yamlAttr.ValidationRules, yamlRule)
		}

		yamlDef.Attributes = append(yamlDef.Attributes, yamlAttr)
	}

	return yamlDef, nil
}

// ImportTypeFromYAML imports a type definition from YAML format
// Note: This does not set the parent type relationship - that needs to be done separately
func ImportTypeFromYAML(yamlDef *TypeDefinitionYAML) (*core.TypeDefinition, error) {
	typeDef := core.NewTypeDefinition(yamlDef.Name, yamlDef.Description)
	// Set the correct version from the YAML
	typeDef.Version = yamlDef.Version

	// Convert attributes
	for _, yamlAttr := range yamlDef.Attributes {
		// Convert data type
		dataType := core.DataType(yamlAttr.DataType)

		// Create attribute
		attr := core.NewAttributeDefinition(
			yamlAttr.Name,
			yamlAttr.Description,
			dataType,
			yamlAttr.Required,
		)

		// Set additional properties
		attr.SetDefaultValue(yamlAttr.DefaultValue)
		attr.SetMultiValued(yamlAttr.MultiValued)
		attr.SetDisabled(yamlAttr.Disabled)

		// Handle multiple cascades if present
		if len(yamlAttr.Cascades) > 0 {
			// Clear existing cascades
			attr.Cascades = make([]core.Cascade, 0, len(yamlAttr.Cascades))

			// Add all cascades from YAML
			for _, yamlCascade := range yamlAttr.Cascades {
				// Create normal cascade
				attr.AddCascade(
					yamlCascade.ID,
					yamlCascade.Enabled,
					core.CascadeBehavior(yamlCascade.Behavior),
					yamlCascade.Logic,
					yamlCascade.Weight,
				)

				// Add validation configuration if present, using the last added cascade
				if yamlCascade.ValidationConfig != nil && len(attr.Cascades) > 0 {
					// Get the last cascade we just added
					cascadeIndex := len(attr.Cascades) - 1

					// Create validation config
					validationConfig := &core.CascadeValidationConfig{
						Action:       core.CascadeValidationAction(yamlCascade.ValidationConfig.Action),
						TargetField:  yamlCascade.ValidationConfig.TargetField,
						Values:       yamlCascade.ValidationConfig.Values,
						StringValue:  yamlCascade.ValidationConfig.StringValue,
						NumericValue: yamlCascade.ValidationConfig.NumericValue,
					}

					// Add validation config to the cascade
					attr.Cascades[cascadeIndex].ValidationConfig = validationConfig
				}
			}
		}

		// Convert validation rules
		for _, yamlRule := range yamlAttr.ValidationRules {
			var rule validation.Rule
			var err error

			switch yamlRule.Type {
			case "required":
				rule = &validation.RequiredRule{}

			case "minLength":
				minLength, ok := yamlRule.Parameters["minLength"].(int)
				if !ok {
					return nil, fmt.Errorf("invalid minLength parameter")
				}
				rule = &validation.MinLengthRule{MinLength: minLength}

			case "maxLength":
				maxLength, ok := yamlRule.Parameters["maxLength"].(int)
				if !ok {
					return nil, fmt.Errorf("invalid maxLength parameter")
				}
				rule = &validation.MaxLengthRule{MaxLength: maxLength}

			case "pattern":
				pattern, ok := yamlRule.Parameters["pattern"].(string)
				if !ok {
					return nil, fmt.Errorf("invalid pattern parameter")
				}
				rule, err = validation.NewPatternRule(pattern)
				if err != nil {
					return nil, err
				}

			case "enum":
				allowedValues, ok := yamlRule.Parameters["allowedValues"].([]interface{})
				if !ok {
					return nil, fmt.Errorf("invalid allowedValues parameter")
				}
				rule = &validation.EnumRule{AllowedValues: allowedValues}

			case "range":
				rangeRule := &validation.RangeRule{}

				if min, ok := yamlRule.Parameters["min"].(float64); ok {
					rangeRule.Min = &min
				}

				if max, ok := yamlRule.Parameters["max"].(float64); ok {
					rangeRule.Max = &max
				}

				rule = rangeRule

			case "generic":
				rule = &validation.GenericRule{}

			default:
				return nil, fmt.Errorf("unsupported validation rule type: %s", yamlRule.Type)
			}

			attr.AddValidationRule(rule)
		}

		typeDef.AddAttribute(attr)
	}

	return typeDef, nil
}

// SaveTypeToYAMLFile saves a type definition to a YAML file
func SaveTypeToYAMLFile(typeDef *core.TypeDefinition, filename string) error {
	yamlDef, err := ExportTypeToYAML(typeDef)
	if err != nil {
		return err
	}

	data, err := yaml.Marshal(yamlDef)
	if err != nil {
		return err
	}

	return ioutil.WriteFile(filename, data, 0644)
}

// LoadTypeFromYAMLFile loads a type definition from a YAML file
func LoadTypeFromYAMLFile(filename string) (*core.TypeDefinition, error) {
	data, err := ioutil.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	var yamlDef TypeDefinitionYAML
	err = yaml.Unmarshal(data, &yamlDef)
	if err != nil {
		return nil, err
	}

	return ImportTypeFromYAML(&yamlDef)
}
