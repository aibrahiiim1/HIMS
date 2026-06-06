// Package agentassets embeds the Relay Agent installer script templates that the
// HIMS API renders (with the per-agent URL/token/site baked in) and bundles into
// the downloadable installer package. Keeping them as embedded templates means
// the scripts are versioned with the server and need no runtime file dependency.
package agentassets

import "embed"

//go:embed *.tmpl
var FS embed.FS
