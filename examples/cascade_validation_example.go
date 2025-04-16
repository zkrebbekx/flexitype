package main

import (
	"context"
	"fmt"
	"log"

	"github.com/zac300/flexitype/internal/adapters/repositories/memory"
	"github.com/zac300/flexitype/internal/domain/core"
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

	// ---- STEP 1: Create a base type definition with valid cascades ----
	fmt.Println("=== Step 1: Creating base type with valid cascades ===")

	// Create a product type
	productType, err := client.CreateType(ctx, "product-001", "Product", "Product record")
	if err != nil {
		log.Fatalf("Failed to create product type: %v", err)
	}

	// Add attributes with cascades
	priceAttr := sdk.NewAttribute("attr-001", "price", "Product price", sdk.FloatType, true)
	priceAttr.AddValidationRule(&sdk.RequiredRule{})

	statusAttr := sdk.NewAttribute("attr-002", "status", "Product status", sdk.StringType, true)
	statusAttr.SetDefaultValue("Active")
	statusRule := sdk.NewEnumRule([]interface{}{"Active", "Discontinued", "OutOfStock"})
	statusAttr.AddValidationRule(statusRule)

	discountAttr := sdk.NewAttribute("attr-003", "discount", "Discount percentage", sdk.FloatType, false)
	discountAttr.AddCascade("price-discount", true, core.CascadeInherit, "price > 1000 => discount = 5.0", 100)

	availableAttr := sdk.NewAttribute("attr-004", "available", "Product is available", sdk.BooleanType, true)
	availableAttr.SetDefaultValue(true)
	availableAttr.AddCascade("status-available", true, core.CascadeInherit, "status == \"Discontinued\" => available = false", 200)

	// Add attributes
	err = client.AddAttribute(ctx, productType.ID, priceAttr)
	if err != nil {
		log.Fatalf("Failed to add price attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType.ID, statusAttr)
	if err != nil {
		log.Fatalf("Failed to add status attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType.ID, discountAttr)
	if err != nil {
		log.Fatalf("Failed to add discount attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType.ID, availableAttr)
	if err != nil {
		log.Fatalf("Failed to add available attribute: %v", err)
	}

	fmt.Println("Successfully created Product type with valid cascades")

	// ---- STEP 2: Try to disable a referenced attribute (should fail) ----
	fmt.Println("\n=== Step 2: Try to disable a referenced attribute (should fail) ===")

	// Try to disable the status attribute which is referenced by available's cascade
	_, err = client.SetAttributeDisabledState(ctx, productType.ID, "attr-002", true)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		log.Fatalf("Expected an error when disabling a referenced attribute, but got none")
	}

	// ---- STEP 3: Try to create a circular dependency (should fail) ----
	fmt.Println("\n=== Step 3: Try to create a circular dependency (should fail) ===")

	// Create a fresh productType to avoid carrying over previous modifications
	productType2, err := client.CreateType(ctx, "product-002", "Product2", "Product record 2")
	if err != nil {
		log.Fatalf("Failed to create product type 2: %v", err)
	}
	
	// Add basic attributes that will form a circular dependency
	price2Attr := sdk.NewAttribute("attr-001", "price", "Product price", sdk.FloatType, true)
	price2Attr.AddValidationRule(&sdk.RequiredRule{})
	
	status2Attr := sdk.NewAttribute("attr-002", "status", "Product status", sdk.StringType, true)
	status2Attr.SetDefaultValue("Active")
	status2Rule := sdk.NewEnumRule([]interface{}{"Active", "Discontinued", "OutOfStock"})
	status2Attr.AddValidationRule(status2Rule)
	
	available2Attr := sdk.NewAttribute("attr-004", "available", "Product is available", sdk.BooleanType, true)
	available2Attr.SetDefaultValue(true)
	
	// Add first two attributes (these should succeed)
	err = client.AddAttribute(ctx, productType2.ID, price2Attr)
	if err != nil {
		log.Fatalf("Failed to add price2 attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType2.ID, status2Attr)
	if err != nil {
		log.Fatalf("Failed to add status2 attribute: %v", err)
	}
	
	// Now create a circular dependency
	available2Attr.AddCascade("status-available", true, core.CascadeInherit, "status == \"Discontinued\" => available = false", 200)
	price2Attr.AddCascade("available-price", true, core.CascadeInherit, "available == false => price = 0.0", 300)
	status2Attr.AddCascade("price-status", true, core.CascadeInherit, "price == 0.0 => status = \"Discontinued\"", 300)
	
	// This should fail due to the circular dependency
	err = client.AddAttribute(ctx, productType2.ID, available2Attr)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		log.Fatalf("Expected an error when creating a circular dependency, but got none")
	}

	// ---- STEP 4: Try to reference a non-existent attribute (should fail) ----
	fmt.Println("\n=== Step 4: Try to reference a non-existent attribute (should fail) ===")

	// Create a fresh product type for this test
	productType3, err := client.CreateType(ctx, "product-003", "Product3", "Product record 3")
	if err != nil {
		log.Fatalf("Failed to create product type 3: %v", err)
	}
	
	// Create an attribute that references a non-existent attribute
	stockAttr := sdk.NewAttribute("attr-005", "stock", "Product stock level", sdk.IntType, true)
	stockAttr.AddCascade("nonexistent", true, core.CascadeInherit, "inventoryCount > 0 => stock = inventoryCount", 100)

	// This should fail because inventoryCount doesn't exist
	err = client.AddAttribute(ctx, productType3.ID, stockAttr)
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		log.Fatalf("Expected an error when referencing a non-existent attribute, but got none")
	}

	// ---- STEP 5: Create a valid complex type with cascades ----
	fmt.Println("\n=== Step 5: Create a valid complex type with cascades ===")

	// Create a fresh product type for this test
	productType4, err := client.CreateType(ctx, "product-004", "Product4", "Product record 4")
	if err != nil {
		log.Fatalf("Failed to create product type 4: %v", err)
	}
	
	// Add base attributes that others will reference
	price4Attr := sdk.NewAttribute("attr-001", "price", "Product price", sdk.FloatType, true)
	err = client.AddAttribute(ctx, productType4.ID, price4Attr)
	if err != nil {
		log.Fatalf("Failed to add price4 attribute: %v", err)
	}
	
	discount4Attr := sdk.NewAttribute("attr-002", "discount", "Discount percentage", sdk.FloatType, false)
	err = client.AddAttribute(ctx, productType4.ID, discount4Attr)
	if err != nil {
		log.Fatalf("Failed to add discount4 attribute: %v", err)
	}

	// Create a valid complex structure with cascades that reference each other but without cycles
	categoryAttr := sdk.NewAttribute("attr-006", "category", "Product category", sdk.StringType, true)
	categoryRule := sdk.NewEnumRule([]interface{}{"Electronics", "Clothing", "Food", "Books"})
	categoryAttr.AddValidationRule(categoryRule)

	taxRateAttr := sdk.NewAttribute("attr-007", "taxRate", "Tax rate percentage", sdk.FloatType, true)
	taxRateAttr.SetDefaultValue(10.0)
	taxRateAttr.AddCascade("category-tax", true, core.CascadeInherit, "category == \"Food\" => taxRate = 5.0", 100)

	shippingAttr := sdk.NewAttribute("attr-008", "shipping", "Shipping cost", sdk.FloatType, true)
	shippingAttr.SetDefaultValue(15.0)
	shippingAttr.AddCascade("price-shipping", true, core.CascadeInherit, "price > 2000 => shipping = 0.0", 100)
	shippingAttr.AddCascade("category-shipping", true, core.CascadeInherit, "category == \"Books\" => shipping = 5.0", 200)

	totalAttr := sdk.NewAttribute("attr-009", "total", "Total price", sdk.FloatType, true)
	totalAttr.AddCascade("calculate-total", true, core.CascadeInherit, 
		"total = price + (price * (taxRate/100)) + shipping - (price * (discount/100))", 300)

	// Add these valid attributes in an order that avoids dependency issues
	err = client.AddAttribute(ctx, productType4.ID, categoryAttr)
	if err != nil {
		log.Fatalf("Failed to add category attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType4.ID, taxRateAttr)
	if err != nil {
		log.Fatalf("Failed to add tax rate attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType4.ID, shippingAttr)
	if err != nil {
		log.Fatalf("Failed to add shipping attribute: %v", err)
	}
	
	err = client.AddAttribute(ctx, productType4.ID, totalAttr)
	if err != nil {
		log.Fatalf("Failed to add total attribute: %v", err)
	}
	
	fmt.Println("Successfully created complex cascade structure with valid dependencies")

	// ---- STEP 6: Try to remove a referenced attribute (should fail) ----
	fmt.Println("\n=== Step 6: Try to remove a referenced attribute (should fail) ===")

	// Try to delete the price attribute which is referenced by multiple cascades
	err = client.DeleteAttribute(ctx, productType4.ID, "attr-001")
	if err != nil {
		fmt.Printf("Expected error: %v\n", err)
	} else {
		log.Fatalf("Expected an error when deleting a referenced attribute, but got none")
	}

	fmt.Println("\nCascade validation example completed successfully")
}