-- +goose Up
-- SQL in this section is executed when the migration is applied

-- Drop existing constraints and indexes that depend on the current primary keys
ALTER TABLE flexitype.attribute_definition DROP CONSTRAINT uq_attr_type_name;
ALTER TABLE flexitype.attribute_definition DROP CONSTRAINT attribute_definition_type_id_fkey;

-- Create temporary indexes to maintain uniqueness during the transition
CREATE UNIQUE INDEX temp_idx_type_name_unique ON flexitype.type_definition(LOWER(name));
CREATE UNIQUE INDEX temp_idx_attr_type_name_unique ON flexitype.attribute_definition(type_id, type_version, LOWER(name));

-- Drop primary key constraints
ALTER TABLE flexitype.type_definition DROP CONSTRAINT type_definition_pkey;
ALTER TABLE flexitype.attribute_definition DROP CONSTRAINT attribute_definition_pkey;

-- Add new primary key for type_definition (name case insensitive)
ALTER TABLE flexitype.type_definition
  ADD PRIMARY KEY (name);
  
-- Create index on ID for lookups and foreign keys
CREATE INDEX idx_type_definition_id ON flexitype.type_definition(id);

-- Add new primary key for attribute_definition (type_name, type_version, attribute_name)
ALTER TABLE flexitype.attribute_definition
  ADD COLUMN type_name VARCHAR(255) NOT NULL DEFAULT '';
  
-- Populate type_name column
UPDATE flexitype.attribute_definition ad
SET type_name = td.name
FROM flexitype.type_definition td
WHERE ad.type_id = td.id;

-- Now set the primary key for attribute_definition
ALTER TABLE flexitype.attribute_definition
  ADD PRIMARY KEY (type_name, type_version, name);

-- Recreate foreign key for attribute_definition to type_definition
ALTER TABLE flexitype.attribute_definition
  ADD CONSTRAINT attribute_definition_type_name_fkey
  FOREIGN KEY (type_name) REFERENCES flexitype.type_definition(name);

-- Update versioned tables similarly
ALTER TABLE flexitype.type_definition_version DROP CONSTRAINT type_definition_version_pkey;
ALTER TABLE flexitype.type_definition_version ADD PRIMARY KEY (name, version);

-- Update the attribute_definition_version table
ALTER TABLE flexitype.attribute_definition_version DROP CONSTRAINT attribute_definition_version_pkey;
ALTER TABLE flexitype.attribute_definition_version ADD COLUMN type_name VARCHAR(255) NOT NULL DEFAULT '';

-- Populate type_name in attribute_definition_version
UPDATE flexitype.attribute_definition_version adv
SET type_name = tdv.name
FROM flexitype.type_definition_version tdv
WHERE adv.type_id = tdv.id AND adv.type_version = tdv.version;

-- Set primary key on attribute_definition_version
ALTER TABLE flexitype.attribute_definition_version
  ADD PRIMARY KEY (type_name, type_version, name);

-- Drop temporary indexes
DROP INDEX flexitype.temp_idx_type_name_unique;
DROP INDEX flexitype.temp_idx_attr_type_name_unique;

-- +goose Down
-- SQL in this section is executed when the migration is rolled back

-- Drop new constraints
ALTER TABLE flexitype.attribute_definition DROP CONSTRAINT attribute_definition_type_name_fkey;
ALTER TABLE flexitype.attribute_definition_version DROP CONSTRAINT attribute_definition_version_pkey;
ALTER TABLE flexitype.type_definition_version DROP CONSTRAINT type_definition_version_pkey;

-- Drop primary keys
ALTER TABLE flexitype.attribute_definition DROP CONSTRAINT attribute_definition_pkey;
ALTER TABLE flexitype.type_definition DROP CONSTRAINT type_definition_pkey;

-- Restore original primary keys
ALTER TABLE flexitype.type_definition ADD PRIMARY KEY (id);
ALTER TABLE flexitype.type_definition_version ADD PRIMARY KEY (id, version);
ALTER TABLE flexitype.attribute_definition ADD PRIMARY KEY (id);
ALTER TABLE flexitype.attribute_definition_version ADD PRIMARY KEY (id, type_id, type_version);

-- Restore original foreign keys and constraints
ALTER TABLE flexitype.attribute_definition
  ADD CONSTRAINT attribute_definition_type_id_fkey
  FOREIGN KEY (type_id) REFERENCES flexitype.type_definition(id);

ALTER TABLE flexitype.attribute_definition
  ADD CONSTRAINT uq_attr_type_name UNIQUE (type_id, name);

-- Drop added columns that are no longer needed
ALTER TABLE flexitype.attribute_definition DROP COLUMN type_name;
ALTER TABLE flexitype.attribute_definition_version DROP COLUMN type_name;

-- Drop index that we added
DROP INDEX IF EXISTS flexitype.idx_type_definition_id;