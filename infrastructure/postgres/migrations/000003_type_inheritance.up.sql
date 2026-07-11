-- Entity-type inheritance: a type may extend one parent type, inheriting
-- its attributes, constraints and dependencies. The pointer is immutable
-- after creation; shadowing rules are enforced in the application layer.

ALTER TABLE flexitype_type_definition
    ADD COLUMN extends_id CHAR(26) REFERENCES flexitype_type_definition (id);

CREATE INDEX idx_flexitype_type_definition_extends
    ON flexitype_type_definition (extends_id)
    WHERE extends_id IS NOT NULL;
