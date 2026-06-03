ALTER TABLE locations DROP CONSTRAINT locations_kind_check;
ALTER TABLE locations ADD CONSTRAINT locations_kind_check
    CHECK (kind IN ('group','hotel','building','floor','area','room','rack'));
