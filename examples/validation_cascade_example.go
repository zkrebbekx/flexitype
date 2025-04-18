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

	// ---- STEP 1: Create a product type with dynamic validation ----
	fmt.Println("=== Step 1: Creating product type with dynamic validation ===")

	// Create a product type
	productType, err := client.SaveType(ctx, "product-001", "Product", "Product record")
	if err != nil {
		log.Fatalf("Failed to create product type: %v", err)
	}

	// Add category attribute with enum values
	categoryAttr := sdk.NewAttribute("attr-001", "category", "Product category", sdk.StringType, true)
	categoryAttr.AddValidationRule(&sdk.RequiredRule{})
	categoryRule := sdk.NewEnumRule([]interface{}{"Electronics", "Clothing", "Furniture", "Appliance", "Other"})
	categoryAttr.AddValidationRule(categoryRule)

	// Add subcategory attribute with dynamic enum values based on category
	subcategoryAttr := sdk.NewAttribute("attr-002", "subcategory", "Product subcategory", sdk.StringType, false)

	// Add price attribute
	priceAttr := sdk.NewAttribute("attr-003", "price", "Product price", sdk.FloatType, true)
	priceAttr.AddValidationRule(&sdk.RequiredRule{})
	min := sdk.Float64Ptr(0.01)
	priceAttr.AddValidationRule(sdk.NewRangeRule(min, nil))

	// Add signature attribute that's required only for expensive items
	signatureAttr := sdk.NewAttribute("attr-004", "managerSignature", "Manager signature", sdk.StringType, false)

	// Add shipping fee attribute with dynamic min/max based on price
	shippingAttr := sdk.NewAttribute("attr-005", "shippingFee", "Shipping fee", sdk.FloatType, false)

	// Add all attributes
	err = client.AddAttribute(ctx, productType.ID, categoryAttr)
	if err != nil {
		log.Fatalf("Failed to add category attribute: %v", err)
	}

	err = client.AddAttribute(ctx, productType.ID, subcategoryAttr)
	if err != nil {
		log.Fatalf("Failed to add subcategory attribute: %v", err)
	}

	err = client.AddAttribute(ctx, productType.ID, priceAttr)
	if err != nil {
		log.Fatalf("Failed to add price attribute: %v", err)
	}

	err = client.AddAttribute(ctx, productType.ID, signatureAttr)
	if err != nil {
		log.Fatalf("Failed to add signature attribute: %v", err)
	}

	err = client.AddAttribute(ctx, productType.ID, shippingAttr)
	if err != nil {
		log.Fatalf("Failed to add shipping attribute: %v", err)
	}

	// ---- STEP 2: Add validation cascades ----
	fmt.Println("\n=== Step 2: Adding validation cascades ===")

	// 1. Define dynamic subcategory values for Electronics
	electronicsRule := &core.CascadeValidationConfig{
		Action:      core.ActionSetEnumValues,
		TargetField: "subcategory",
		Values:      []interface{}{"Smartphone", "Laptop", "TV", "Camera", "Audio"},
	}

	// Create a helper function to evaluate attribute equality
	checkElectronics := func(instance *core.Instance) (bool, error) {
		value, err := instance.GetAttribute("category")
		if err != nil {
			return false, fmt.Errorf("failed to get category attribute: %w", err)
		}
		return value == "Electronics", nil
	}

	electronicsExpr := core.NewCustomExpression(checkElectronics)
	categoryAttr.AddValidationCascadeWithCustomExpr(
		"electronics-subcategories",
		true,
		core.CascadeEnumValues,
		electronicsExpr,
		500,
		electronicsRule,
	)

	// 2. Define dynamic subcategory values for Appliance
	applianceRule := &core.CascadeValidationConfig{
		Action:      core.ActionSetEnumValues,
		TargetField: "subcategory",
		Values:      []interface{}{"Refrigerator", "Microwave", "Dishwasher", "Washing Machine"},
	}

	// Create a helper function to evaluate attribute equality
	checkAppliance := func(instance *core.Instance) (bool, error) {
		value, err := instance.GetAttribute("category")
		if err != nil {
			return false, fmt.Errorf("failed to get category attribute: %w", err)
		}
		return value == "Appliance", nil
	}

	applianceExpr := core.NewCustomExpression(checkAppliance)
	categoryAttr.AddValidationCascadeWithCustomExpr(
		"appliance-subcategories",
		true,
		core.CascadeEnumValues,
		applianceExpr,
		500,
		applianceRule,
	)

	// 3. Define dynamic subcategory values for Furniture
	furnitureRule := &core.CascadeValidationConfig{
		Action:      core.ActionSetEnumValues,
		TargetField: "subcategory",
		Values:      []interface{}{"Chair", "Table", "Sofa", "Bed", "Cabinet"},
	}

	// Create a helper function to evaluate attribute equality
	checkFurniture := func(instance *core.Instance) (bool, error) {
		value, err := instance.GetAttribute("category")
		if err != nil {
			return false, fmt.Errorf("failed to get category attribute: %w", err)
		}
		return value == "Furniture", nil
	}

	furnitureExpr := core.NewCustomExpression(checkFurniture)
	categoryAttr.AddValidationCascadeWithCustomExpr(
		"furniture-subcategories",
		true,
		core.CascadeEnumValues,
		furnitureExpr,
		500,
		furnitureRule,
	)

	// 4. Make signature required for expensive items (price > 500)
	signatureRule := &core.CascadeValidationConfig{
		Action:      core.ActionMakeRequired,
		TargetField: "managerSignature",
	}

	// Create a helper function to evaluate price comparison
	checkExpensive := func(instance *core.Instance) (bool, error) {
		priceValue, err := instance.GetAttribute("price")
		if err != nil {
			return false, fmt.Errorf("failed to get price attribute: %w", err)
		}

		price, ok := priceValue.(float64)
		if !ok {
			return false, fmt.Errorf("price is not a float64")
		}

		return price > 500, nil
	}

	expensiveExpr := core.NewCustomExpression(checkExpensive)
	priceAttr.AddValidationCascadeWithCustomExpr(
		"expensive-signature",
		true,
		core.CascadeRequirement,
		expensiveExpr,
		400,
		signatureRule,
	)

	// 5. Set shipping fee range based on price
	lowPriceShippingRule := &core.CascadeValidationConfig{
		Action:       core.ActionSetMaxValue,
		TargetField:  "shippingFee",
		NumericValue: 10.0,
	}

	// Create a helper function to evaluate price comparison for low price items
	checkLowPrice := func(instance *core.Instance) (bool, error) {
		priceValue, err := instance.GetAttribute("price")
		if err != nil {
			return false, fmt.Errorf("failed to get price attribute: %w", err)
		}

		price, ok := priceValue.(float64)
		if !ok {
			return false, fmt.Errorf("price is not a float64")
		}

		return price < 50, nil
	}

	lowPriceExpr := core.NewCustomExpression(checkLowPrice)
	priceAttr.AddValidationCascadeWithCustomExpr(
		"low-price-shipping",
		true,
		core.CascadeValidation,
		lowPriceExpr,
		300,
		lowPriceShippingRule,
	)

	// 6. Free shipping for expensive items
	freeShippingRule := &core.CascadeValidationConfig{
		Action:       core.ActionSetMaxValue,
		TargetField:  "shippingFee",
		NumericValue: 0.0,
	}

	// Create a helper function to evaluate price comparison for free shipping
	checkFreeShipping := func(instance *core.Instance) (bool, error) {
		priceValue, err := instance.GetAttribute("price")
		if err != nil {
			return false, fmt.Errorf("failed to get price attribute: %w", err)
		}

		price, ok := priceValue.(float64)
		if !ok {
			return false, fmt.Errorf("price is not a float64")
		}

		return price >= 200, nil
	}

	freeShippingExpr := core.NewCustomExpression(checkFreeShipping)
	priceAttr.AddValidationCascadeWithCustomExpr(
		"free-shipping",
		true,
		core.CascadeValidation,
		freeShippingExpr,
		500,
		freeShippingRule,
	)

	// Update the type definition with the cascades
	err = typeRepo.Save(ctx, productType)
	if err != nil {
		log.Fatalf("Failed to save type with cascades: %v", err)
	}

	fmt.Println("Added validation cascades successfully")

	// ---- STEP 3: Create instances to demonstrate dynamic validation ----
	fmt.Println("\n=== Step 3: Testing instances with dynamic validation ===")

	// Test 1: Electronics product with appropriate subcategory
	electronicsAttrs := map[string]interface{}{
		"category":         "Electronics",
		"subcategory":      "Laptop",
		"price":            799.99,
		"managerSignature": "John Doe", // Required because price > 500
	}

	electronicsProd, err := client.SaveInstance(ctx, "prod-001", productType, electronicsAttrs)
	if err != nil {
		log.Fatalf("Failed to create electronics product: %v", err)
	}

	fmt.Println("Created electronics product successfully")
	fmt.Printf("- Category: %v\n", electronicsProd.Attributes["category"])
	fmt.Printf("- Subcategory: %v\n", electronicsProd.Attributes["subcategory"])
	fmt.Printf("- Price: $%.2f\n", electronicsProd.Attributes["price"])
	fmt.Printf("- Manager Signature: %v\n", electronicsProd.Attributes["managerSignature"])

	// Test 2: Try to create an electronics product with invalid subcategory (should fail)
	invalidElectronicsAttrs := map[string]interface{}{
		"category":    "Electronics",
		"subcategory": "Refrigerator", // Invalid for Electronics category
		"price":       499.99,
	}

	_, err = client.SaveInstance(ctx, "prod-002", productType, invalidElectronicsAttrs)
	if err != nil {
		fmt.Printf("Expected validation error: %v\n", err)
	} else {
		log.Fatalf("Expected validation error for invalid subcategory, but none occurred")
	}

	// Test 3: Try to create an expensive product without signature (should fail)
	expensiveNoSignatureAttrs := map[string]interface{}{
		"category":    "Furniture",
		"subcategory": "Sofa",
		"price":       1299.99,
		// Missing managerSignature which should be required for price > 500
	}

	_, err = client.SaveInstance(ctx, "prod-003", productType, expensiveNoSignatureAttrs)
	if err != nil {
		fmt.Printf("Expected validation error: %v\n", err)
	} else {
		log.Fatalf("Expected validation error for missing signature, but none occurred")
	}

	// Test 4: Try to set invalid shipping fee for expensive item (should fail)
	expensiveAttrs := map[string]interface{}{
		"category":         "Furniture",
		"subcategory":      "Sofa",
		"price":            1299.99,
		"managerSignature": "Jane Smith",
		"shippingFee":      15.99, // This should fail because price >= 200 means free shipping (max 0)
	}

	_, err = client.SaveInstance(ctx, "prod-004", productType, expensiveAttrs)
	if err != nil {
		fmt.Printf("Expected validation error: %v\n", err)
	} else {
		log.Fatalf("Expected validation error for invalid shipping fee, but none occurred")
	}

	// Test 5: Create valid Appliance product
	applianceAttrs := map[string]interface{}{
		"category":    "Appliance",
		"subcategory": "Microwave",
		"price":       149.99,
		"shippingFee": 9.99, // Valid shipping fee for this price range
	}

	applianceProd, err := client.SaveInstance(ctx, "prod-005", productType, applianceAttrs)
	if err != nil {
		log.Fatalf("Failed to create appliance product: %v", err)
	}

	fmt.Println("\nCreated appliance product successfully")
	fmt.Printf("- Category: %v\n", applianceProd.Attributes["category"])
	fmt.Printf("- Subcategory: %v\n", applianceProd.Attributes["subcategory"])
	fmt.Printf("- Price: $%.2f\n", applianceProd.Attributes["price"])
	fmt.Printf("- Shipping Fee: $%.2f\n", applianceProd.Attributes["shippingFee"])

	fmt.Println("\nDynamic validation cascade example completed successfully")
}
