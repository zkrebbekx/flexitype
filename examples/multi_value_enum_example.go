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

	// ---- STEP 1: Create a product type with multi-valued enum attributes ----
	fmt.Println("=== Step 1: Creating product type with multi-valued enum attributes ===")

	// Create a product type
	productType, err := client.SaveType(ctx, "product-001", "Product", "Product item with categories and tags")
	if err != nil {
		log.Fatalf("Failed to create product type: %v", err)
	}

	// Add basic attributes
	nameAttr := sdk.NewAttribute("attr-001", "name", "Product name", sdk.StringType, true)
	nameAttr.AddValidationRule(&sdk.RequiredRule{})

	priceAttr := sdk.NewAttribute("attr-002", "price", "Product price", sdk.FloatType, true)
	priceAttr.AddValidationRule(&sdk.RequiredRule{})

	minValue := sdk.Float64Ptr(0.01)
	rangeRule := sdk.NewRangeRule(minValue, nil)
	priceAttr.AddValidationRule(rangeRule)

	// Add multi-valued category attribute with enum validation
	categoryAttr := sdk.NewAttribute("attr-003", "categories", "Product categories", sdk.StringType, true)
	categoryAttr.SetMultiValued(true) // Allow multiple values

	// Create a validation rule for allowed category values
	categoryRule := sdk.NewEnumRule([]interface{}{
		"Electronics", "Computers", "Smartphones", "Audio", "Cameras",
		"Home", "Kitchen", "Furniture", "Decor", "Appliances",
		"Clothing", "Shoes", "Accessories", "Outerwear", "Sportswear",
	})
	categoryAttr.AddValidationRule(categoryRule)

	// Add multi-valued tags with enum validation and default values
	tagsAttr := sdk.NewAttribute("attr-004", "tags", "Product tags", sdk.StringType, false)
	tagsAttr.SetMultiValued(true) // Allow multiple values

	// Create a validation rule for allowed tag values
	tagsRule := sdk.NewEnumRule([]interface{}{
		"New", "Featured", "Sale", "Clearance", "Limited", "Exclusive",
		"Premium", "Budget", "Bestseller", "Trending", "Seasonal",
	})
	tagsAttr.AddValidationRule(tagsRule)

	// Add a cascade to add "Premium" tag if price is above threshold
	tagsAttr.AddCascade("price-over-500", true, core.CascadeInherit, "price > 500 => tags = \"Premium\"", 300)

	// Add a cascade to add "Exclusive" tag if product has limited stock
	tagsAttr.AddCascade("limited-stock", true, core.CascadeInherit, "stockQuantity < 50 && stockQuantity > 0 => tags = \"Limited\"", 400)

	// Add a cascade to add "Clearance" tag if stock is very low
	tagsAttr.AddCascade("clearance-stock", true, core.CascadeInherit, "stockQuantity < 10 && stockQuantity > 0 => tags = \"Clearance\"", 500)

	// Add stock quantity attribute
	stockAttr := sdk.NewAttribute("attr-005", "stockQuantity", "Number of items in stock", sdk.IntType, true)
	stockAttr.AddValidationRule(&sdk.RequiredRule{})
	stockAttr.SetDefaultValue(0)

	// Add all attributes to the type
	client.AddAttribute(ctx, productType.ID, nameAttr)
	client.AddAttribute(ctx, productType.ID, priceAttr)
	client.AddAttribute(ctx, productType.ID, categoryAttr)
	client.AddAttribute(ctx, productType.ID, tagsAttr)
	client.AddAttribute(ctx, productType.ID, stockAttr)

	// Save the type
	err = typeRepo.Save(ctx, productType)
	if err != nil {
		log.Fatalf("Failed to save product type: %v", err)
	}

	fmt.Printf("Created Product type with %d attributes\n", len(productType.Attributes))
	for _, attr := range productType.Attributes {
		fmt.Printf("- %s (%s)", attr.Name, attr.DataType)
		if attr.MultiValued {
			fmt.Printf(" [Multi-Valued]")
		}
		if attr.HasEnabledCascades() {
			for _, cascade := range attr.GetCascades() {
				fmt.Printf("\n  [Cascade Weight=%d, Logic: %s]", cascade.Weight, cascade.Logic)
			}
		}
		fmt.Println()
	}

	// ---- STEP 2: Create specialized product type ----
	fmt.Println("\n=== Step 2: Creating specialized product type ===")

	// Create a specialized product type
	electronicsType, err := client.SaveType(ctx, "product-002", "ElectronicsProduct", "Electronics product with specialized attributes")
	if err != nil {
		log.Fatalf("Failed to create electronics type: %v", err)
	}

	// Set parent type
	electronicsType.SetParentType(productType)

	// Add specialized attributes
	warrantyAttr := sdk.NewAttribute("attr-006", "warrantyMonths", "Warranty period in months", sdk.IntType, true)
	warrantyAttr.SetDefaultValue(12)

	// Add specialized multi-valued attribute for technical specifications
	specsAttr := sdk.NewAttribute("attr-007", "specifications", "Technical specifications", sdk.StringType, false)
	specsAttr.SetMultiValued(true)

	// Add condition to set "Premium" tag if warranty is more than 24 months
	tagsOverrideAttr := sdk.NewAttribute("attr-004", "tags", "Product tags", sdk.StringType, false)
	tagsOverrideAttr.SetMultiValued(true)
	tagsOverrideAttr.AddValidationRule(tagsRule) // Reuse the same enum rule

	// Add a higher-weight cascade for warranty-based premium tag
	tagsOverrideAttr.AddCascade("2-year-waranty-premium", true, core.CascadeInherit, "warrantyMonths > 24 => tags = \"Premium\"", 600)

	// Add to type
	client.AddAttribute(ctx, electronicsType.ID, warrantyAttr)
	client.AddAttribute(ctx, electronicsType.ID, specsAttr)
	client.AddAttribute(ctx, electronicsType.ID, tagsOverrideAttr)

	// Save the type
	err = typeRepo.Save(ctx, electronicsType)
	if err != nil {
		log.Fatalf("Failed to save electronics type: %v", err)
	}

	fmt.Printf("Created ElectronicsProduct type inheriting from Product\n")
	fmt.Printf("All attributes (including inherited): %d\n", len(electronicsType.GetAllAttributes()))

	for _, attr := range electronicsType.GetAllAttributes() {
		fmt.Printf("- %s (%s)", attr.Name, attr.DataType)
		if attr.MultiValued {
			fmt.Printf(" [Multi-Valued]")
		}
		if attr.HasEnabledCascades() {
			for _, cascade := range attr.GetCascades() {
				fmt.Printf("\n  [Cascade Weight=%d, Logic: %s]", cascade.Weight, cascade.Logic)
			}
		}
		fmt.Println()
	}

	// ---- STEP 3: Create product instances with multi-valued attributes ----
	fmt.Println("\n=== Step 3: Creating product instances with multi-valued enum attributes ===")

	// Test Case 1: Standard product with multiple categories and manually specified tags
	standardProductAttrs := map[string]interface{}{
		"name":          "Standard Office Desk",
		"price":         299.99,
		"categories":    []interface{}{"Home", "Furniture"},
		"tags":          []interface{}{"New", "Featured"},
		"stockQuantity": 100,
	}

	standardProduct, err := client.SaveInstance(ctx, "product-inst-001", productType, standardProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create standard product: %v", err)
	}

	fmt.Println("Standard Product ($299.99, 100 in stock):")
	printMultiValuedAttributes(standardProduct)

	// Test Case 2: Premium product with price-based tag addition
	premiumProductAttrs := map[string]interface{}{
		"name":          "Premium Executive Desk",
		"price":         899.99,
		"categories":    []interface{}{"Home", "Furniture", "Premium"},
		"tags":          []interface{}{"New"},
		"stockQuantity": 75,
	}

	premiumProduct, err := client.SaveInstance(ctx, "product-inst-002", productType, premiumProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create premium product: %v", err)
	}

	fmt.Println("\nPremium Product ($899.99, 75 in stock):")
	printMultiValuedAttributes(premiumProduct)

	// Test Case 3: Limited stock product (should add Limited tag)
	limitedProductAttrs := map[string]interface{}{
		"name":          "Designer Lamp",
		"price":         129.99,
		"categories":    []interface{}{"Home", "Decor"},
		"tags":          []interface{}{"New"},
		"stockQuantity": 35,
	}

	limitedProduct, err := client.SaveInstance(ctx, "product-inst-003", productType, limitedProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create limited product: %v", err)
	}

	fmt.Println("\nLimited Product ($129.99, 35 in stock):")
	printMultiValuedAttributes(limitedProduct)

	// Test Case 4: Clearance product (very low stock, should add Clearance tag)
	clearanceProductAttrs := map[string]interface{}{
		"name":          "Vintage Bookshelf",
		"price":         199.99,
		"categories":    []interface{}{"Home", "Furniture"},
		"tags":          []interface{}{"Sale"},
		"stockQuantity": 5,
	}

	clearanceProduct, err := client.SaveInstance(ctx, "product-inst-004", productType, clearanceProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create clearance product: %v", err)
	}

	fmt.Println("\nClearance Product ($199.99, 5 in stock):")
	printMultiValuedAttributes(clearanceProduct)

	// Test Case 5: Electronic product with extended warranty (should add Premium tag through higher-weight cascade)
	laptopProductAttrs := map[string]interface{}{
		"name":           "Ultrabook Pro",
		"price":          1299.99,
		"categories":     []interface{}{"Electronics", "Computers"},
		"tags":           []interface{}{"New", "Featured"},
		"stockQuantity":  50,
		"warrantyMonths": 36,
		"specifications": []interface{}{"16GB RAM", "1TB SSD", "Intel i7", "14-inch Display"},
	}

	laptopProduct, err := client.SaveInstance(ctx, "product-inst-005", electronicsType, laptopProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create laptop product: %v", err)
	}

	fmt.Println("\nLaptop with Extended Warranty ($1299.99, 36-month warranty):")
	printMultiValuedAttributes(laptopProduct)

	// Test Case 6: Electronic product with standard warranty and low stock
	phoneProductAttrs := map[string]interface{}{
		"name":           "Smartphone XL",
		"price":          699.99,
		"categories":     []interface{}{"Electronics", "Smartphones"},
		"tags":           []interface{}{"New"},
		"stockQuantity":  8,
		"warrantyMonths": 12,
		"specifications": []interface{}{"6GB RAM", "128GB Storage", "6.5-inch OLED", "48MP Camera"},
	}

	phoneProduct, err := client.SaveInstance(ctx, "product-inst-006", electronicsType, phoneProductAttrs)
	if err != nil {
		log.Fatalf("Failed to create phone product: %v", err)
	}

	fmt.Println("\nSmartphone with Low Stock ($699.99, 8 in stock):")
	printMultiValuedAttributes(phoneProduct)
}

// Helper function to print multi-valued attributes
func printMultiValuedAttributes(instance *core.Instance) {
	// Print basic info
	name, _ := instance.GetAttribute("name")
	price, _ := instance.GetAttribute("price")
	stock, _ := instance.GetAttribute("stockQuantity")
	fmt.Printf("Name: %v\n", name)
	fmt.Printf("Price: $%.2f\n", price)
	fmt.Printf("Stock: %v\n", stock)

	// Print categories (multi-valued)
	categories, err := instance.GetAttribute("categories")
	if err == nil {
		if catArray, ok := categories.([]interface{}); ok {
			fmt.Printf("Categories (%d): %v\n", len(catArray), catArray)
		} else {
			fmt.Printf("Categories: %v\n", categories)
		}
	}

	// Print tags (multi-valued with cascade-added values)
	tags, err := instance.GetAttribute("tags")
	if err == nil {
		if tagArray, ok := tags.([]interface{}); ok {
			fmt.Printf("Tags (%d): %v\n", len(tagArray), tagArray)
		} else {
			fmt.Printf("Tags: %v\n", tags)
		}
	}

	// Print specifications if it exists (only for electronics products)
	specs, err := instance.GetAttribute("specifications")
	if err == nil {
		if specsArray, ok := specs.([]interface{}); ok {
			fmt.Printf("Specifications (%d): %v\n", len(specsArray), specsArray)
		} else {
			fmt.Printf("Specifications: %v\n", specs)
		}
	}

	// Print warranty if it exists
	warranty, err := instance.GetAttribute("warrantyMonths")
	if err == nil {
		fmt.Printf("Warranty: %v months\n", warranty)
	}
}
