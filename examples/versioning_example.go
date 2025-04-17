package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/jmoiron/sqlx"
	_ "github.com/lib/pq"
	"github.com/zac300/flexitype/internal/adapters/repositories/postgres"
	"github.com/zac300/flexitype/internal/domain/core"
	"github.com/zac300/flexitype/internal/domain/validation"
)

// This example demonstrates the comprehensive versioning capabilities 
// for both type definitions and instances
func main() {
	// Connect to PostgreSQL
	db, err := sqlx.Connect("postgres", "user=postgres password=postgres dbname=postgres sslmode=disable")
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.Close()

	// Create PostgreSQL repository
	repo := postgres.NewPostgresRepository(db)
	typeRepo := postgres.NewTypeRepository(repo)
	instanceRepo := postgres.NewInstanceRepository(repo, typeRepo)
	
	ctx := context.Background()

	// Clean up from previous runs
	cleanupPreviousRun(ctx, db)

	// Part 1: Create a type definition (Version 1)
	fmt.Println("=== Part 1: Creating Type Definition (Version 1) ===")
	productType := createProductTypeV1()
	
	if err := typeRepo.Save(ctx, productType); err != nil {
		log.Fatalf("Failed to save type definition: %v", err)
	}
	fmt.Printf("Created product type v1: %s (version %d)\n", productType.ID, productType.Version)

	// Part 2: Create instances of the type (Version 1)
	fmt.Println("\n=== Part 2: Creating Instances (Version 1) ===")
	instances := createInstancesV1(productType)
	
	for _, instance := range instances {
		if err := instanceRepo.Save(ctx, instance); err != nil {
			log.Fatalf("Failed to save instance: %v", err)
		}
		fmt.Printf("Created instance: %s (version %d)\n", instance.ID, instance.Version)
	}

	// Part 3: Evolve the type definition (Version 2)
	fmt.Println("\n=== Part 3: Evolving Type Definition (Version 2) ===")
	productType = evolveTypeToV2(ctx, typeRepo, productType)
	fmt.Printf("Updated product type to v2: %s (version %d)\n", productType.ID, productType.Version)

	// Part 4: Create new instance versions with the updated type
	fmt.Println("\n=== Part 4: Creating New Instance Versions ===")
	updateInstancesToV2(ctx, instanceRepo, instances, productType)

	// Part 5: Verify we can retrieve specific versions
	fmt.Println("\n=== Part 5: Retrieving Specific Versions ===")
	retrieveAndVerifyVersions(ctx, typeRepo, instanceRepo, productType.ID)

	// Part 6: Verify versioned attribute values
	fmt.Println("\n=== Part 6: Verifying Attribute Values Across Versions ===")
	verifyAttributeValues(ctx, instanceRepo, instances[0].ID)
	
	fmt.Println("\nVersioning example completed successfully!")
}

// cleanupPreviousRun removes data from previous runs to ensure clean execution
func cleanupPreviousRun(ctx context.Context, db *sqlx.DB) {
	// Delete test data by ID pattern
	db.ExecContext(ctx, "DELETE FROM flexitype.attribute_value WHERE instance_id LIKE 'test-product-%'")
	db.ExecContext(ctx, "DELETE FROM flexitype.instance WHERE id LIKE 'test-product-%'")
	db.ExecContext(ctx, "DELETE FROM flexitype.attribute_cascade WHERE attribute_id LIKE 'product-type:%'")
	db.ExecContext(ctx, "DELETE FROM flexitype.validation_rule WHERE attribute_id LIKE 'product-type:%'")
	db.ExecContext(ctx, "DELETE FROM flexitype.attribute_definition WHERE type_id = 'product-type'")
	db.ExecContext(ctx, "DELETE FROM flexitype.type_definition WHERE id = 'product-type'")
	
	// Also clean up versioned tables
	db.ExecContext(ctx, "DELETE FROM flexitype.attribute_cascade_version WHERE type_id = 'product-type'")
	db.ExecContext(ctx, "DELETE FROM flexitype.validation_rule_version WHERE type_id = 'product-type'")
	db.ExecContext(ctx, "DELETE FROM flexitype.attribute_definition_version WHERE type_id = 'product-type'")
	db.ExecContext(ctx, "DELETE FROM flexitype.type_definition_version WHERE type_id = 'product-type'")
}

// createProductTypeV1 creates the initial version of the Product type definition
func createProductTypeV1() *core.TypeDefinition {
	// Create a new product type definition
	productType := core.NewTypeDefinition("product-type", "Product", "A sellable product")

	// Add name attribute
	nameAttr := core.NewAttributeDefinition(
		"product-type:name:1",
		"name",
		"Product name",
		core.DataTypeString,
		true, // required
	)
	nameAttr.AddValidationRule(&validation.StringLengthRule{
		Min: 2,
		Max: 100,
	})
	productType.AddAttribute(nameAttr)

	// Add price attribute
	priceAttr := core.NewAttributeDefinition(
		"product-type:price:1",
		"price",
		"Product price",
		core.DataTypeFloat,
		true, // required
	)
	productType.AddAttribute(priceAttr)

	// Add description attribute
	descAttr := core.NewAttributeDefinition(
		"product-type:description:1",
		"description",
		"Product description",
		core.DataTypeString,
		false, // optional
	)
	productType.AddAttribute(descAttr)
	
	// Add category attribute
	categoryAttr := core.NewAttributeDefinition(
		"product-type:category:1",
		"category",
		"Product category",
		core.DataTypeString,
		true, // required
	)
	categoryAttr.AddValidationRule(&validation.EnumRule{
		AllowedValues: []interface{}{"Electronics", "Clothing", "Food", "Books"},
	})
	productType.AddAttribute(categoryAttr)

	return productType
}

// createInstancesV1 creates initial instances of the product type
func createInstancesV1(productType *core.TypeDefinition) []*core.Instance {
	instances := make([]*core.Instance, 0)

	// Create first product instance
	product1 := core.NewInstance("test-product-1", productType)
	product1.SetAttribute("name", "Smartphone")
	product1.SetAttribute("price", 599.99)
	product1.SetAttribute("description", "High-end smartphone with great features")
	product1.SetAttribute("category", "Electronics")
	instances = append(instances, product1)

	// Create second product instance
	product2 := core.NewInstance("test-product-2", productType)
	product2.SetAttribute("name", "T-Shirt")
	product2.SetAttribute("price", 19.99)
	product2.SetAttribute("description", "Cotton t-shirt")
	product2.SetAttribute("category", "Clothing")
	instances = append(instances, product2)

	return instances
}

// evolveTypeToV2 updates the type definition to version 2
func evolveTypeToV2(ctx context.Context, typeRepo *postgres.TypeRepositoryImpl, productType *core.TypeDefinition) *core.TypeDefinition {
	// First retrieve the latest version to ensure we're working with current data
	latestType, err := typeRepo.GetByID(ctx, productType.ID)
	if err != nil {
		log.Fatalf("Failed to get latest type: %v", err)
	}
	
	// Increment the version
	latestType.IncrementVersion()
	
	// Add new in_stock attribute
	inStockAttr := core.NewAttributeDefinition(
		fmt.Sprintf("%s:in_stock:%d", latestType.ID, latestType.Version),
		"in_stock",
		"Whether the product is in stock",
		core.DataTypeBoolean,
		true, // required
	)
	latestType.AddAttribute(inStockAttr)
	
	// Modify existing category attribute by adding new value
	categoryAttr := latestType.GetAttributeByName("category")
	if categoryAttr != nil {
		for i, rule := range categoryAttr.ValidationRules {
			if enumRule, ok := rule.(*validation.EnumRule); ok {
				// Add a new category option
				enumRule.AllowedValues = append(enumRule.AllowedValues, "Home & Garden")
				categoryAttr.ValidationRules[i] = enumRule
				break
			}
		}
	}
	
	// Add tags as a multi-valued attribute
	tagsAttr := core.NewAttributeDefinition(
		fmt.Sprintf("%s:tags:%d", latestType.ID, latestType.Version),
		"tags",
		"Product tags",
		core.DataTypeString,
		false, // optional
	)
	tagsAttr.SetMultiValued(true)
	latestType.AddAttribute(tagsAttr)
	
	// Save the updated type definition
	if err := typeRepo.Save(ctx, latestType); err != nil {
		log.Fatalf("Failed to save updated type: %v", err)
	}
	
	return latestType
}

// updateInstancesToV2 creates new versions of existing instances with the updated type
func updateInstancesToV2(ctx context.Context, instanceRepo *postgres.InstanceRepositoryImpl, instances []*core.Instance, updatedType *core.TypeDefinition) {
	for _, instance := range instances {
		// Get the latest instance
		latestInstance, err := instanceRepo.GetByID(ctx, instance.ID)
		if err != nil {
			log.Fatalf("Failed to get latest instance: %v", err)
		}
		
		// Create a new version
		newVersion := latestInstance.Version + 1
		instanceV2 := core.NewInstanceVersion(latestInstance, newVersion)
		
		// Update type and type version
		instanceV2.TypeDefinition = updatedType
		instanceV2.TypeVersion = updatedType.Version
		
		// Copy existing attributes
		for name, value := range latestInstance.Attributes {
			instanceV2.Attributes[name] = value
		}
		
		// Set new attributes for v2
		instanceV2.SetAttribute("in_stock", true)
		
		if instanceV2.ID == "test-product-1" {
			instanceV2.SetAttribute("tags", []interface{}{"premium", "5G", "Android"})
		} else if instanceV2.ID == "test-product-2" {
			instanceV2.SetAttribute("tags", []interface{}{"cotton", "medium", "casual"})
		}
		
		// Save the new instance version
		if err := instanceRepo.Save(ctx, instanceV2); err != nil {
			log.Fatalf("Failed to save instance v2: %v", err)
		}
		
		fmt.Printf("Created instance v2: %s (version %d)\n", instanceV2.ID, instanceV2.Version)
	}
}

// retrieveAndVerifyVersions verifies we can retrieve specific versions
func retrieveAndVerifyVersions(ctx context.Context, typeRepo *postgres.TypeRepositoryImpl, instanceRepo *postgres.InstanceRepositoryImpl, typeID string) {
	// Verify type versions
	typeV1, err := typeRepo.GetByIDAndVersion(ctx, typeID, 1)
	if err != nil {
		log.Fatalf("Failed to get type v1: %v", err)
	}
	fmt.Printf("Retrieved type v1: %s (version %d) with %d attributes\n", 
		typeV1.ID, typeV1.Version, len(typeV1.Attributes))
	
	typeV2, err := typeRepo.GetByIDAndVersion(ctx, typeID, 2)
	if err != nil {
		log.Fatalf("Failed to get type v2: %v", err)
	}
	fmt.Printf("Retrieved type v2: %s (version %d) with %d attributes\n", 
		typeV2.ID, typeV2.Version, len(typeV2.Attributes))
	
	// Verify instance versions
	instanceV1, err := instanceRepo.GetByIDAndVersion(ctx, "test-product-1", 1)
	if err != nil {
		log.Fatalf("Failed to get instance v1: %v", err)
	}
	fmt.Printf("Retrieved instance v1: %s (version %d, type version %d)\n", 
		instanceV1.ID, instanceV1.Version, instanceV1.TypeVersion)
	
	instanceV2, err := instanceRepo.GetByIDAndVersion(ctx, "test-product-1", 2)
	if err != nil {
		log.Fatalf("Failed to get instance v2: %v", err)
	}
	fmt.Printf("Retrieved instance v2: %s (version %d, type version %d)\n", 
		instanceV2.ID, instanceV2.Version, instanceV2.TypeVersion)
	
	// Get all versions of an instance
	allVersions, err := instanceRepo.GetAllVersions(ctx, "test-product-1")
	if err != nil {
		log.Fatalf("Failed to get all versions: %v", err)
	}
	fmt.Printf("Retrieved all %d versions of instance test-product-1\n", len(allVersions))
}

// verifyAttributeValues checks that attribute values are stored and retrieved correctly across versions
func verifyAttributeValues(ctx context.Context, instanceRepo *postgres.InstanceRepositoryImpl, instanceID string) {
	// Retrieve instance v1 
	instanceV1, err := instanceRepo.GetByIDAndVersion(ctx, instanceID, 1)
	if err != nil {
		log.Fatalf("Failed to get instance v1: %v", err)
	}
	
	// Print attribute values for v1
	fmt.Println("Instance v1 attributes:")
	for name, value := range instanceV1.Attributes {
		fmt.Printf("  %s: %v\n", name, value)
	}
	
	// Retrieve instance v2
	instanceV2, err := instanceRepo.GetByIDAndVersion(ctx, instanceID, 2)
	if err != nil {
		log.Fatalf("Failed to get instance v2: %v", err)
	}
	
	// Print attribute values for v2
	fmt.Println("Instance v2 attributes:")
	for name, value := range instanceV2.Attributes {
		fmt.Printf("  %s: %v\n", name, value)
	}
	
	// Verify v2 has new attributes that v1 doesn't have
	_, hasInStockV1 := instanceV1.Attributes["in_stock"]
	_, hasInStockV2 := instanceV2.Attributes["in_stock"]
	_, hasTagsV1 := instanceV1.Attributes["tags"]
	_, hasTagsV2 := instanceV2.Attributes["tags"]
	
	fmt.Printf("Attribute 'in_stock' exists in v1: %v, in v2: %v\n", hasInStockV1, hasInStockV2)
	fmt.Printf("Attribute 'tags' exists in v1: %v, in v2: %v\n", hasTagsV1, hasTagsV2)
}