package app

func (a *App) SetProjectAutoAcceptPermissions(enabled bool) (string, error) {
	path := ProjectLocalConfigPath(a.workspaceRoot)
	cfg, _, err := LoadConfigFile(path)
	if err != nil {
		return "", err
	}
	cfg.Permissions.AutoAccept = &enabled
	if err := SaveConfigFile(path, cfg); err != nil {
		return "", err
	}
	a.SetAutoAcceptPermissions(enabled)
	return path, nil
}
