DROP TABLE IF EXISTS wireless_controller_summary;
ALTER TABLE ssh_cli_results DROP COLUMN IF EXISTS warnings;
ALTER TABLE ssh_cli_results DROP COLUMN IF EXISTS skipped_rows;
ALTER TABLE ssh_cli_results DROP COLUMN IF EXISTS headers;
ALTER TABLE ssh_cli_results DROP COLUMN IF EXISTS line_count;
