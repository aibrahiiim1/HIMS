-- Opt-in hierarchical scope for Relay Agents.
--
-- Agent->site routing matches the device's location EXACTLY, so an agent
-- assigned to a parent group serves nothing in its child hotels. That is a trap
-- (group-level assignment looks correct and silently covers no devices), but
-- changing the default would silently widen the blast radius of every existing
-- agent — an agent that today serves one site would start receiving jobs for
-- every site beneath it.
--
-- So inheritance is OPT-IN and OFF by default: every existing agent keeps its
-- current exact-only behaviour after this migration, and an operator turns it on
-- per agent when they actually want a group-level collector.
ALTER TABLE relay_agents
    ADD COLUMN IF NOT EXISTS include_descendants BOOLEAN NOT NULL DEFAULT false;

COMMENT ON COLUMN relay_agents.include_descendants IS
    'When true this agent may also serve devices in sites BENEATH its assigned location. Default false = exact-site matching only. A directly assigned child-site agent always wins over an inherited parent agent.';
