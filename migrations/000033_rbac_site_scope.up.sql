-- RBAC (#23): scope a user to a site. NULL location_id = global (all sites);
-- otherwise the user is bound to one hotel/site, which the multi-site views and
-- a future request-authorization layer use to limit what they see.
ALTER TABLE users ADD COLUMN location_id UUID REFERENCES locations(id) ON DELETE SET NULL;
