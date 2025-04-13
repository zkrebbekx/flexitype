-- Create type_definition table
CREATE TABLE type_definition (
    id UUID PRIMARY KEY,
    internal_name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

-- Create attribute_definition table
CREATE TABLE attribute_definition (
    id UUID PRIMARY KEY,
    internal_name VARCHAR(255) NOT NULL,
    display_name VARCHAR(255) NOT NULL,
    description TEXT,
    type_definition_id UUID NOT NULL REFERENCES type_definition(id),
    type VARCHAR(50) NOT NULL,
    constraints JSONB NOT NULL DEFAULT '{}'::jsonb,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

-- Create attribute_value_* tables for different types
CREATE TABLE attribute_value_string (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value TEXT NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_bool (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value BOOLEAN NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_integer (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value INTEGER NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_float (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value DOUBLE PRECISION NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_date (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value DATE NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_time (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value TIME NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_enum (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_decimal (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value DECIMAL(20, 6) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_url (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value VARCHAR(2048) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_email (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value VARCHAR(255) NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

CREATE TABLE attribute_value_json (
    id UUID PRIMARY KEY,
    attribute_definition_id UUID NOT NULL REFERENCES attribute_definition(id),
    value JSONB NOT NULL,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE
);

-- Create attribute_link table
CREATE TABLE attribute_link (
    id UUID PRIMARY KEY,
    source_attribute_id UUID NOT NULL REFERENCES attribute_definition(id),
    target_attribute_id UUID NOT NULL REFERENCES attribute_definition(id),
    link_type VARCHAR(50) NOT NULL,
    description TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT source_target_different CHECK (source_attribute_id != target_attribute_id)
);

-- Create attribute_value_dependency table
CREATE TABLE attribute_value_dependency (
    id UUID PRIMARY KEY,
    source_attribute_id UUID NOT NULL REFERENCES attribute_definition(id),
    source_conditions JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_attribute_id UUID NOT NULL REFERENCES attribute_definition(id),
    target_values JSONB NOT NULL DEFAULT '[]'::jsonb,
    target_constraints JSONB NOT NULL DEFAULT '[]'::jsonb,
    description TEXT,
    version INTEGER NOT NULL DEFAULT 1,
    created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT CURRENT_TIMESTAMP,
    archived_at TIMESTAMP WITH TIME ZONE,
    CONSTRAINT source_target_different CHECK (source_attribute_id != target_attribute_id)
);

-- Create indexes
CREATE INDEX idx_type_definition_internal_name ON type_definition(internal_name);
CREATE INDEX idx_attribute_definition_internal_name ON attribute_definition(internal_name);
CREATE INDEX idx_attribute_definition_type_definition_id ON attribute_definition(type_definition_id);
CREATE INDEX idx_attribute_value_string_attribute_definition_id ON attribute_value_string(attribute_definition_id);
CREATE INDEX idx_attribute_value_bool_attribute_definition_id ON attribute_value_bool(attribute_definition_id);
CREATE INDEX idx_attribute_value_integer_attribute_definition_id ON attribute_value_integer(attribute_definition_id);
CREATE INDEX idx_attribute_value_float_attribute_definition_id ON attribute_value_float(attribute_definition_id);
CREATE INDEX idx_attribute_value_date_attribute_definition_id ON attribute_value_date(attribute_definition_id);
CREATE INDEX idx_attribute_value_time_attribute_definition_id ON attribute_value_time(attribute_definition_id);
CREATE INDEX idx_attribute_value_enum_attribute_definition_id ON attribute_value_enum(attribute_definition_id);
CREATE INDEX idx_attribute_value_decimal_attribute_definition_id ON attribute_value_decimal(attribute_definition_id);
CREATE INDEX idx_attribute_value_url_attribute_definition_id ON attribute_value_url(attribute_definition_id);
CREATE INDEX idx_attribute_value_email_attribute_definition_id ON attribute_value_email(attribute_definition_id);
CREATE INDEX idx_attribute_value_json_attribute_definition_id ON attribute_value_json(attribute_definition_id);
CREATE INDEX idx_attribute_link_source_attribute_id ON attribute_link(source_attribute_id);
CREATE INDEX idx_attribute_link_target_attribute_id ON attribute_link(target_attribute_id);
CREATE INDEX idx_attribute_value_dependency_source_attribute_id ON attribute_value_dependency(source_attribute_id);
CREATE INDEX idx_attribute_value_dependency_target_attribute_id ON attribute_value_dependency(target_attribute_id); 