package pluginabi

const (
	ABIVersion    uint32 = 1
	SchemaVersion uint32 = 1
)

const (
	MethodPluginRegister     = "plugin.register"
	MethodPluginReconfigure  = "plugin.reconfigure"
	MethodUsageHandle        = "usage.handle"
	MethodSchedulerPick      = "scheduler.pick"
	MethodManagementRegister = "management.register"
	MethodManagementHandle   = "management.handle"
)
