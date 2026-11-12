package keymap

// systemActions are the global actions about other computers, the daemon
// itself and the coding agents (internal/tui/remote).
func systemActions(global func(group, id, keys, help string) *Action) {
	const group = "Computers and daemon"
	global(group, "computers", "C", "Other computers").Menu = true
	on := global(group, "on", "", "Switch to another computer (:on local returns)")
	on.Arg, on.Optional = ArgComputer, true
	pair := global(group, "pair", "", "Pair with another computer (asks for its code)")
	pair.Arg, pair.Optional = ArgComputer, true
	global(group, "daemon", "", "Daemon details").Menu = true
	upgrade := global(group, "upgrade", "", "Upgrade the daemon in place")
	upgrade.Menu, upgrade.Destructive = true, true
	restart := global(group, "restart-daemon", "", "Restart the daemon in place")
	restart.Menu, restart.Destructive = true, true
	global(group, "setup", "", "Set up coding agents").Menu = true
	global(group, "doctor", "", "Check the setup (runs doctor)")
}
