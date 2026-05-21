package service

func onOff(v bool) string {
	if v {
		return "on"
	}
	return "off"
}

func autoAcceptMessage(enabled bool) string {
	if enabled {
		return "Session auto-accept enabled"
	}
	return "Session auto-accept disabled"
}
