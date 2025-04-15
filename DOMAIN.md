# FlexiType Domain Model

FlexiType is a dynamic type system that allows you to define custom types with dynamic attributes and validation rules. This document explains the core domain concepts and how they relate to each other.

## Core Domain Concepts

### 1. TypeDefinition

A `TypeDefinition` represents a custom type with its own set of attributes. It's analogous to a class in object-oriented programming or a database schema.

Key features:
- Each type has a unique ID, name, and description
- Types are versioned - version number increments when the type definition changes
- Types can inherit from other types (parent-child relationship)
- Types can have multiple attributes
- Child types inherit all attributes from their parent types
- Child types can override attributes inherited from parents

### 2. AttributeDefinition

An `AttributeDefinition` represents a property or field that can be assigned to a type. It defines what kind of data can be stored and how it should be validated.

Key features:
- Each attribute has a unique ID, name, and description
- Attributes have a specific data type (string, int, float, boolean, date, object, array)
- Attributes can be marked as required
- Attributes can have a default value
- Attributes can have multiple validation rules
- Attributes can be marked as Cascades (they will be inherited by child types)
- Attributes can be marked as multi-valued (can store multiple values)
- Attributes can be disabled/enabled between type versions (making them unusable until re-enabled)

### 3. ValidationRule

A `ValidationRule` defines a constraint that must be satisfied by an attribute value. Rules are used to ensure data integrity and consistency.

Built-in rule types:
- Required: Value must not be nil or empty
- MinLength: String must have at least N characters
- MaxLength: String must have at most N characters
- Pattern: String must match a regular expression
- Enum: Value must be one of a predefined set of values
- Range: Numeric value must be within a specified range
- Custom: Custom validation logic provided by the user

### 4. Instance

An `Instance` represents a concrete object of a specific type with actual values for its attributes. It's analogous to an object instance in object-oriented programming or a database record.

Key features:
- Each instance has a unique ID
- Instances are associated with a specific type definition and track the version of the type
- Instances store attribute values as key-value pairs
- All required attributes must have values
- All attribute values must satisfy their validation rules
- Instances can be migrated to newer versions of their type definition

## Type Versioning and Migration

FlexiType implements a version control system for type definitions:

1. Type Versioning
   - Each type definition has a version number that starts at 1
   - The version number is incremented whenever the type definition changes (attributes added/removed, etc.)
   - Version history is tracked in the type definition itself

2. Instance Migration
   - Instances track which version of a type definition they were created with
   - When accessing or updating an instance, it's automatically migrated to the latest type version
   - Migration validates that the instance meets all requirements of the new type version
   - If migration fails (e.g., new required attributes are missing), the operation fails with detailed error messages

3. Validation During Migration
   - When a type adds new required attributes, existing instances need to provide these values
   - When type validation rules change, existing values are validated against the new rules
   - Changes that don't affect validation (like adding optional attributes) allow seamless migration

## Type Inheritance and Cascades

FlexiType supports a powerful inheritance model using what we call "Cascades":

1. Type Inheritance
   - Child types inherit attributes from parent types that are marked as Cascades
   - Child types can override, modify, or disable inherited Cascades
   - Inheritance is resolved recursively (grandparent Cascades cascade to grandchildren)

2. Cascade Attributes
   - Attributes can be marked as Cascades that are inherited by child types
   - Cascades can carry logic expressions that affect other attributes
   - Cascades have configurable behavior (inherit, override, disabled)
   - This provides fine-grained control over inheritance behavior

3. Cascade Behaviors
   - CascadeInherit: Child types inherit the cascade as-is
   - CascadeOverride: Child types can override the cascade with their own version
   - CascadeDisabled: Child types can disable specific cascades from parents

4. Cascade Logic Expression Engine
   - Cascades can include sophisticated logical expressions with multiple operators
   - The expression engine supports:
     - Logical operators: AND (&&), OR (||), NOT (!)
     - Comparison operators: ==, !=, >, <, >=, <=
     - Parentheses for grouping: (...)
     - Special functions: isEmpty(attr), isNotEmpty(attr)
     - Consequences: condition => action
   - Examples of supported expressions:
     - Simple: `amount > 1000 => signatureRequired = true`
     - Complex: `(amount > 5000) || (vendor == "HighRisk" && amount > 500) => signatureRequired = true`
     - With functions: `status == "Rejected" && isEmpty(rejectionReason) => rejectionReason = "Please provide a reason"`
     - With parentheses: `(isNotEmpty(vendorApprovalRequired) && vendorApprovalRequired == true) || amount > 10000 => requiresReview = true`
   - Logic is evaluated during instance validation
   - When a condition is true, it can automatically set other attribute values
   - This enables powerful dynamic behaviors like:
     - Conditional required fields
     - Linked/cascading dropdowns
     - Automatic value propagation
     - Cross-field validations
     - Business rules enforcement

This approach allows for flexible extension of existing types without modifying the original definitions.

## Real-World Applications

### Product Catalog

A product catalog might define a base "Product" type with common attributes like name, SKU, price, and description. Specific product categories can then be modeled as child types:

- `Product` (base type)
  - `Electronics` extends `Product` (adds attributes like warranty, voltage)
    - `Laptop` extends `Electronics` (adds attributes like CPU, RAM, storage)
    - `Smartphone` extends `Electronics` (adds attributes like screen size, camera)
  - `Clothing` extends `Product` (adds attributes like size, color, material)
    - `Shirt` extends `Clothing` (adds attributes like sleeve length, collar type)
    - `Pants` extends `Clothing` (adds attributes like waist size, inseam)

### Document Management

A document management system might define types for various document categories:

- `Document` (base type)
  - `Legal` extends `Document` (adds attributes like jurisdiction, practice area)
  - `Financial` extends `Document` (adds attributes like fiscal year, currency)
  - `Technical` extends `Document` (adds attributes like product version, technology stack)

## Implementation Approach

FlexiType follows Domain-Driven Design (DDD) principles with a clean/hexagonal architecture:

1. **Rich Domain Model**
   - The domain model is the heart of the system
   - Business logic lives in the domain layer
   - Entities are not just data holders; they contain behavior

2. **Ports and Adapters**
   - The application defines interfaces (ports) for interacting with external systems
   - Concrete implementations (adapters) provide the actual functionality
   - This allows easy swapping of components (e.g., storage backends)

3. **Layered Architecture**
   - Domain Layer: Core business logic and entities
   - Application Layer: Orchestrates domain objects to perform use cases
   - Infrastructure Layer: Provides technical capabilities
   - Interface Layer: Connects to the outside world (APIs, UIs)

This architecture ensures that the system is modular, testable, and adaptable to changing requirements.