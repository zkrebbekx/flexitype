package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/pkg/sdk"
)

func main() {
	// Initialize repositories
	typeRepo := memory.NewInMemoryTypeRepository()
	instanceRepo := memory.NewInMemoryInstanceRepository()

	// Create SDK client
	client := sdk.NewClient(typeRepo, instanceRepo)

	// Create context
	ctx := context.Background()

	// ---- STEP 1: Create initial type definition (v1) ----
	fmt.Println("=== Step 1: Creating initial type definition (v1) ===")

	customerType, err := client.CreateType(ctx, "customer-001", "Customer", "Customer record")
	if err != nil {
		log.Fatalf("Failed to create customer type: %v", err)
	}

	// Add initial attributes
	nameAttr := sdk.NewAttribute("attr-001", "name", "Customer name", sdk.StringType, true)
	nameAttr.AddValidationRule(&sdk.RequiredRule{})

	emailAttr := sdk.NewAttribute("attr-002", "email", "Customer email", sdk.StringType, true)
	emailAttr.AddValidationRule(&sdk.RequiredRule{})
	patternRule, err := sdk.NewPatternRule(".+@.+\\..+")
	if err != nil {
		log.Fatalf("Failed to create email pattern rule: %v", err)
	}
	emailAttr.AddValidationRule(patternRule)

	client.AddAttribute(ctx, customerType.ID, nameAttr)
	client.AddAttribute(ctx, customerType.ID, emailAttr)

	// Save the type in the repository
	err = typeRepo.Save(ctx, customerType)
	if err != nil {
		log.Fatalf("Failed to save customer type: %v", err)
	}

	fmt.Printf("Created Customer type v%d with %d attributes\n",
		customerType.Version, len(customerType.Attributes))

	// ---- STEP 2: Create an instance of the type (v1) ----
	fmt.Println("\n=== Step 2: Creating instance with type v1 ===")

	customerAttrs := map[string]interface{}{
		"name":  "John Doe",
		"email": "john.doe@example.com",
	}

	customer, err := client.CreateInstance(ctx, "cust-1001", customerType, customerAttrs)
	if err != nil {
		log.Fatalf("Failed to create customer instance: %v", err)
	}

	fmt.Printf("Created customer instance: %s\n", customer.ID)
	fmt.Printf("Using type version: %d\n", customer.TypeVersion)
	for name, value := range customer.Attributes {
		fmt.Printf("Attribute %s = %v\n", name, value)
	}

	// ---- STEP 3: Update the type definition (creates v2) ----
	fmt.Println("\n=== Step 3: Updating type definition to v2 ===")

	// Refresh our type reference
	customerType, err = client.GetType(ctx, "customer-001")
	if err != nil {
		log.Fatalf("Failed to get customer type: %v", err)
	}

	// Add a new required attribute
	phoneAttr := sdk.NewAttribute("attr-003", "phone", "Customer phone number", sdk.StringType, true)
	phoneAttr.AddValidationRule(&sdk.RequiredRule{})
	phonePattern, err := sdk.NewPatternRule("^\\+?[0-9]{10,15}$")
	if err != nil {
		log.Fatalf("Failed to create phone pattern rule: %v", err)
	}
	phoneAttr.AddValidationRule(phonePattern)

	client.AddAttribute(ctx, customerType.ID, phoneAttr)

	// Get the updated type
	customerType, err = client.GetType(ctx, "customer-001")
	if err != nil {
		log.Fatalf("Failed to get updated customer type: %v", err)
	}

	fmt.Printf("Updated Customer type to v%d with %d attributes\n",
		customerType.Version, len(customerType.Attributes))

	// ---- STEP 4: Try to update the instance (should fail due to version mismatch) ----
	fmt.Println("\n=== Step 4: Attempting to update instance (will fail due to version mismatch) ===")

	customerUpdateAttrs := map[string]interface{}{
		"name": "John Smith",
	}

	_, err = client.UpdateInstance(ctx, "cust-1001", customerUpdateAttrs)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		log.Fatalf("Update should have failed due to version mismatch")
	}

	// ---- STEP 5: Update the instance with the required new attribute ----
	fmt.Println("\n=== Step 5: Updating instance with all required attributes ===")

	customerUpdateAttrs = map[string]interface{}{
		"name":  "John Smith",
		"phone": "+1234567890",
	}

	updatedCustomer, err := client.UpdateInstance(ctx, "cust-1001", customerUpdateAttrs)
	if err != nil {
		log.Fatalf("Failed to update customer instance: %v", err)
	}

	fmt.Printf("Updated customer instance: %s\n", updatedCustomer.ID)
	fmt.Printf("Now using type version: %d\n", updatedCustomer.TypeVersion)
	for name, value := range updatedCustomer.Attributes {
		fmt.Printf("Attribute %s = %v\n", name, value)
	}

	// ---- STEP 6: Update the type again (creates v3) with a non-required attribute ----
	fmt.Println("\n=== Step 6: Updating type definition to v3 with non-required attribute ===")

	// Refresh our type reference
	customerType, err = client.GetType(ctx, "customer-001")
	if err != nil {
		log.Fatalf("Failed to get customer type: %v", err)
	}

	// Add a new non-required attribute
	addressAttr := sdk.NewAttribute("attr-004", "address", "Customer address", sdk.StringType, false)

	client.AddAttribute(ctx, customerType.ID, addressAttr)

	// Get the updated type
	customerType, err = client.GetType(ctx, "customer-001")
	if err != nil {
		log.Fatalf("Failed to get updated customer type: %v", err)
	}

	fmt.Printf("Updated Customer type to v%d with %d attributes\n",
		customerType.Version, len(customerType.Attributes))

	// ---- STEP 7: Update the instance (should succeed without address) ----
	fmt.Println("\n=== Step 7: Updating instance (should succeed without address) ===")

	customerUpdateAttrs = map[string]interface{}{
		"name": "John A. Smith",
	}

	updatedCustomer, err = client.UpdateInstance(ctx, "cust-1001", customerUpdateAttrs)
	if err != nil {
		log.Fatalf("Failed to update customer instance: %v", err)
	}

	fmt.Printf("Updated customer instance: %s\n", updatedCustomer.ID)
	fmt.Printf("Now using type version: %d\n", updatedCustomer.TypeVersion)
	for name, value := range updatedCustomer.Attributes {
		fmt.Printf("Attribute %s = %v\n", name, value)
	}
}
