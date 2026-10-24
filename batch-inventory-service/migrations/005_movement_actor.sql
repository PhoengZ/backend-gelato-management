-- Historical actors are unknown; preserve NULL instead of inventing attribution.
ALTER TABLE inventory_movements ADD COLUMN actor_id uuid;
ALTER TABLE inventory_movements ADD COLUMN actor_role text;
ALTER TABLE inventory_movements ADD CONSTRAINT movement_actor_pair CHECK ((actor_id IS NULL) = (actor_role IS NULL));
ALTER TABLE inventory_movements ADD CONSTRAINT movement_actor_role CHECK (actor_role IN ('STAFF','MANAGER'));
ALTER TABLE inventory_movements ADD CONSTRAINT movement_actor_nonzero CHECK (actor_id IS NULL OR actor_id <> '00000000-0000-0000-0000-000000000000'::uuid);
