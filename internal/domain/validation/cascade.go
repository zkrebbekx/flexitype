package validation

import (
	"fmt"
	"regexp"
	"strings"
)

// CascadeValidator provides validation for cascades to ensure they reference valid attributes
// and don't create circular dependencies
type CascadeValidator struct {
	// Map of attribute name to list of dependencies it has
	dependencies map[string][]string
	// Map of attribute name to enabled status
	attributeStatus map[string]bool
}

// NewCascadeValidator creates a new CascadeValidator
func NewCascadeValidator() *CascadeValidator {
	return &CascadeValidator{
		dependencies:    make(map[string][]string),
		attributeStatus: make(map[string]bool),
	}
}

// RegisterAttribute registers an attribute's existence and status (enabled/disabled)
func (v *CascadeValidator) RegisterAttribute(name string, enabled bool) {
	v.attributeStatus[name] = enabled
}

// RegisterCascade registers a cascade logic expression and the attribute it belongs to
func (v *CascadeValidator) RegisterCascade(attributeName, logic string) {
	// Extract dependencies from the logic expression
	deps := v.extractDependencies(logic)
	
	// Register dependencies for this attribute
	v.dependencies[attributeName] = append(v.dependencies[attributeName], deps...)
}

// Validate performs validation on all registered cascades
// Returns a list of validation errors
func (v *CascadeValidator) Validate() []error {
	errors := make([]error, 0)
	
	// Validate attribute references
	for attr, deps := range v.dependencies {
		for _, dep := range deps {
			// Check if the referenced attribute exists
			enabled, exists := v.attributeStatus[dep]
			
			if !exists {
				errors = append(errors, fmt.Errorf(
					"attribute '%s' references non-existent attribute '%s' in cascade logic",
					attr, dep))
				continue
			}
			
			// Check if the referenced attribute is enabled
			if !enabled {
				errors = append(errors, fmt.Errorf(
					"attribute '%s' references disabled attribute '%s' in cascade logic",
					attr, dep))
			}
		}
	}
	
	// Check for circular dependencies
	circularErrors := v.detectCircularDependencies()
	errors = append(errors, circularErrors...)
	
	return errors
}

// extractDependencies extracts attribute dependencies from a logic expression
// This is a sophisticated implementation that handles various expression formats
func (v *CascadeValidator) extractDependencies(logic string) []string {
	// Common keywords and operators to exclude from attribute name detection
	keywords := map[string]bool{
		"true": true, "false": true, "null": true, "undefined": true,
		"if": true, "then": true, "else": true, "for": true, "in": true,
		"and": true, "or": true, "not": true, "=": true, "==": true, "===": true, 
		"!=": true, "!==": true, ">": true, "<": true, ">=": true, "<=": true,
		"isEmpty": true, "isNotEmpty": true, "=>": true,
	}
	
	// First, remove string literals to avoid matching their contents as attributes
	// Match both single and double quoted strings
	stringLiterals := regexp.MustCompile(`["']([^"'\\]|\\.)*["']`)
	logicWithoutStrings := stringLiterals.ReplaceAllString(logic, "STRING_LITERAL")
	
	// Match potential attribute names (words starting with a letter or underscore)
	attributeRegex := regexp.MustCompile(`\b[a-zA-Z_]\w*\b`)
	
	// Find all potential attribute names in the cleaned logic
	matches := attributeRegex.FindAllString(logicWithoutStrings, -1)
	
	// Filter out function calls by checking if they're followed by an opening parenthesis
	filteredMatches := make([]string, 0, len(matches))
	for _, match := range matches {
		if match != "STRING_LITERAL" {
			// Check if this match is a function call (followed by parenthesis)
			funcPattern := regexp.MustCompile(match + `\s*\(`)
			if !funcPattern.MatchString(logicWithoutStrings) {
				filteredMatches = append(filteredMatches, match)
			}
		}
	}
	
	matches = filteredMatches
	
	// Filter out keywords and duplicates
	uniqueDeps := make(map[string]bool)
	for _, match := range matches {
		if !keywords[strings.ToLower(match)] {
			uniqueDeps[match] = true
		}
	}
	
	// Convert to slice
	result := make([]string, 0, len(uniqueDeps))
	for dep := range uniqueDeps {
		result = append(result, dep)
	}
	
	return result
}

// detectCircularDependencies checks for circular dependencies in the registered cascades
func (v *CascadeValidator) detectCircularDependencies() []error {
	errors := make([]error, 0)
	
	// Visited nodes for each starting point
	visited := make(map[string]bool)
	
	// Path stack for cycle detection
	path := make([]string, 0)
	
	// Process each attribute as a potential starting point
	for attr := range v.dependencies {
		// Reset visited and path for each starting attribute
		for k := range visited {
			visited[k] = false
		}
		path = path[:0]
		
		// Perform DFS to detect cycles
		if err := v.dfs(attr, attr, visited, path); err != nil {
			errors = append(errors, err)
		}
	}
	
	return errors
}

// dfs performs depth-first search to detect circular dependencies
// startAttr: The attribute we started from (to detect cycles back to start)
// currentAttr: The current attribute we're examining
// visited: Map of visited attributes in the current DFS path
// path: Current path being explored
func (v *CascadeValidator) dfs(startAttr, currentAttr string, visited map[string]bool, path []string) error {
	// Check if we've completed a cycle back to the start
	if currentAttr == startAttr && len(path) > 0 {
		// We've found a cycle, report it as an error
		cyclePath := append(append([]string{}, path...), startAttr)
		return fmt.Errorf("circular cascade dependency detected: %s", strings.Join(cyclePath, " → "))
	}
	
	// If we've already visited this node in the current path, stop recursion
	if visited[currentAttr] {
		return nil
	}
	
	// Mark as visited
	visited[currentAttr] = true
	
	// Add to path
	newPath := append(path, currentAttr)
	
	// Visit all dependencies if this attribute exists and is enabled
	if enabled, exists := v.attributeStatus[currentAttr]; exists && enabled {
		for _, dep := range v.dependencies[currentAttr] {
			// Skip self-references (attributes that reference themselves)
			if dep == currentAttr {
				continue
			}
			
			// Recursively explore this dependency
			if err := v.dfs(startAttr, dep, visited, newPath); err != nil {
				return err
			}
		}
	}
	
	// Unmark as visited when backtracking
	visited[currentAttr] = false
	
	return nil
}