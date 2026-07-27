-- Restore the per-row trigger from 000019, deadlock class and all.
DROP TRIGGER IF EXISTS flexitype_entity_summary_ins ON flexitype_attribute_value;
DROP TRIGGER IF EXISTS flexitype_entity_summary_upd ON flexitype_attribute_value;
DROP TRIGGER IF EXISTS flexitype_entity_summary_del ON flexitype_attribute_value;

DROP FUNCTION IF EXISTS flexitype_entity_summary_ins_trg();
DROP FUNCTION IF EXISTS flexitype_entity_summary_upd_trg();
DROP FUNCTION IF EXISTS flexitype_entity_summary_del_trg();

CREATE TRIGGER flexitype_entity_summary_maintain
    AFTER INSERT OR UPDATE OR DELETE ON flexitype_attribute_value
    FOR EACH ROW EXECUTE FUNCTION flexitype_entity_summary_trg();
