package utils

// Entities mirrors the TS `entities` constant (entities.utils.ts) — a registry
// of all known entity names organized by app/domain. Use Entities[domain] to
// access nested groupings; values are empty maps that act as type tags.
//
// This is kept in sync with the TS export so downstream code that switches on
// entity names sees the same set on both sides.
var Entities = map[string]map[string]map[string]struct{}{
	"common": {
		"assets": {
			"file": {},
		},
		"capital": {
			"account":     {},
			"payment":     {},
			"paymentLog":  {},
			"plan":        {},
			"planType":    {},
			"provider":    {},
			"rate":        {},
			"transaction": {},
			"usage":       {},
			"voucher":     {},
			"voucherType": {},
			"wallet":      {},
		},
		"common": {
			"ip":          {},
			"project":     {},
			"projectType": {},
			"rate":        {},
			"rateLog":     {},
			"tag":         {},
			"tagType":     {},
		},
		"controller": {
			"route": {},
		},
		"figs": {
			"file": {},
		},
		"health": {
			"log":     {},
			"service": {},
			"summary": {},
		},
		"identity": {
			"attribute":           {},
			"device":              {},
			"deviceLog":           {},
			"deviceSession":       {},
			"invite":              {},
			"permission":          {},
			"permissionType":      {},
			"client":              {},
			"clientLog":           {},
			"provider":            {},
			"providerLog":         {},
			"role":                {},
			"roleType":            {},
			"token":               {},
			"user":                {},
			"userClientWorkspace": {},
			"workspace":           {},
		},
		"statics": {
			"list": {},
		},
	},
	"origine": {
		"dashboard": {
			"appVar":          {},
			"deployment":      {},
			"deploymentEvent": {},
			"deploymentLog":   {},
			"domain":          {},
			"domainContact":   {},
			"domainDns":       {},
			"domainLog":       {},
			"domainNs":        {},
			"instance":        {},
			"nodeType":        {},
			"port":            {},
			"record":          {},
			"snapshot":        {},
			"snapshotRestore": {},
			"source":          {},
			"sourceLog":       {},
			"sourceNamespace": {},
			"sourceResource":  {},
			"sourceTag":       {},
			"sourceType":      {},
			"var":             {},
			"varType":         {},
			"zone":            {},
		},
		"watchman": {},
	},
	"polylog": {
		"audit": {
			"log": {},
		},
		"dashboard": {
			"event":      {},
			"eventLog":   {},
			"channel":    {},
			"sink":       {},
			"sinkType":   {},
			"source":     {},
			"sourceType": {},
		},
		"fuss": {
			"history":  {},
			"token":    {},
			"tokenLog": {},
		},
		"notification": {
			"log":      {},
			"message":  {},
			"rule":     {},
			"template": {},
		},
	},
	"qonsole": {
		"dashboard": {
			"attribute":    {},
			"datacenter":   {},
			"license":      {},
			"leicenseType": {},
			"plan":         {},
			"planType":     {},
			"preference":   {},
			"server":       {},
			"user":         {},
			"webhook":      {},
		},
	},
}
