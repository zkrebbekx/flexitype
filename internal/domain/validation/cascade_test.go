package validation

import (
	"testing"
)

func TestCascadeValidator_ExtractDependencies(t *testing.T) {
	validator := NewCascadeValidator()

	testCases := []struct {
		name     string
		logic    string
		expected []string
	}{
		{
			name:     "Simple attribute reference",
			logic:    "price > 1000",
			expected: []string{"price"},
		},
		{
			name:     "Multiple attributes",
			logic:    "price > 1000 && quantity < 5",
			expected: []string{"price", "quantity"},
		},
		{
			name:     "With function call",
			logic:    "isEmpty(rejectionReason) && status == \"Rejected\"",
			expected: []string{"rejectionReason", "status"},
		},
		{
			name:     "Complex expression",
			logic:    "(amount > 1000) || (vendor == \"HighRisk\" && amount > 500) => signatureRequired = true",
			expected: []string{"amount", "vendor", "signatureRequired"},
		},
		{
			name:     "With property access and complex nesting",
			logic:    "customer.type == \"VIP\" && (price * quantity > 5000 || hasDiscount) => applyDiscount(10)",
			expected: []string{"customer", "type", "price", "quantity", "hasDiscount", "applyDiscount"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			deps := validator.extractDependencies(tc.logic)
			
			// Check if all expected dependencies are found
			for _, expected := range tc.expected {
				found := false
				for _, dep := range deps {
					if dep == expected {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("Expected dependency '%s' not found in extracted dependencies: %v", expected, deps)
				}
			}
		})
	}
}

func TestCascadeValidator_DetectCircularDependencies(t *testing.T) {
	testCases := []struct {
		name           string
		dependencies   map[string][]string
		expectedErrors int
	}{
		{
			name: "No circular dependencies",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3"},
				"attr2": {"attr4"},
				"attr3": {"attr4"},
				"attr4": {},
			},
			expectedErrors: 0,
		},
		{
			name: "Simple circular dependency",
			dependencies: map[string][]string{
				"attr1": {"attr2"},
				"attr2": {"attr1"},
			},
			expectedErrors: 1, // One circular path: attr1 -> attr2 -> attr1
		},
		{
			name: "Complex circular dependency",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3"},
				"attr2": {"attr4"},
				"attr3": {"attr4"},
				"attr4": {"attr1"},  // Creates a cycle
			},
			expectedErrors: 1, // One circular path: attr1 -> attr2/attr3 -> attr4 -> attr1
		},
		{
			name: "Multiple circular dependencies",
			dependencies: map[string][]string{
				"attr1": {"attr2"},
				"attr2": {"attr3"},
				"attr3": {"attr1", "attr4"}, // Cycle 1: attr1 -> attr2 -> attr3 -> attr1
				"attr4": {"attr5"},
				"attr5": {"attr4"}, // Cycle 2: attr4 -> attr5 -> attr4
			},
			expectedErrors: 2, // Two circular paths
		},
		{
			name: "Self-reference (should be ignored)",
			dependencies: map[string][]string{
				"attr1": {"attr1"},  // Self-reference
				"attr2": {"attr3"},
				"attr3": {},
			},
			expectedErrors: 0, // Self-references are ignored
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			validator := NewCascadeValidator()
			
			// Set up the dependencies
			for attr, deps := range tc.dependencies {
				validator.dependencies[attr] = deps
				validator.attributeStatus[attr] = true // Enable all attributes
			}
			
			// Detect circular dependencies
			errors := validator.detectCircularDependencies()
			
			if len(errors) != tc.expectedErrors {
				t.Errorf("Expected %d circular dependency errors, got %d: %v", 
					tc.expectedErrors, len(errors), errors)
			}
		})
	}
}

func TestCascadeValidator_ValidateAttributeReferences(t *testing.T) {
	testCases := []struct {
		name            string
		dependencies    map[string][]string
		attributeStatus map[string]bool
		expectedErrors  int
	}{
		{
			name: "All attributes exist and are enabled",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3"},
				"attr2": {"attr3"},
			},
			attributeStatus: map[string]bool{
				"attr1": true,
				"attr2": true,
				"attr3": true,
			},
			expectedErrors: 0,
		},
		{
			name: "Reference to non-existent attribute",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3"},
				"attr2": {"attr4"}, // attr4 doesn't exist
			},
			attributeStatus: map[string]bool{
				"attr1": true,
				"attr2": true,
				"attr3": true,
			},
			expectedErrors: 1, // One error for attr4 not existing
		},
		{
			name: "Reference to disabled attribute",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3"},
				"attr2": {"attr3"},
			},
			attributeStatus: map[string]bool{
				"attr1": true,
				"attr2": true,
				"attr3": false, // attr3 exists but is disabled
			},
			expectedErrors: 2, // Two errors: attr1->attr3 and attr2->attr3
		},
		{
			name: "Multiple validation errors",
			dependencies: map[string][]string{
				"attr1": {"attr2", "attr3", "attr4"}, // attr4 doesn't exist
				"attr2": {"attr3"},
			},
			attributeStatus: map[string]bool{
				"attr1": true,
				"attr2": true,
				"attr3": false, // attr3 exists but is disabled
			},
			expectedErrors: 3, // Three errors: attr1->attr3, attr1->attr4, attr2->attr3
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			validator := NewCascadeValidator()
			
			// Set up the validator
			validator.dependencies = tc.dependencies
			validator.attributeStatus = tc.attributeStatus
			
			// Validate
			errors := validator.Validate()
			
			if len(errors) != tc.expectedErrors {
				t.Errorf("Expected %d validation errors, got %d: %v", 
					tc.expectedErrors, len(errors), errors)
			}
		})
	}
}