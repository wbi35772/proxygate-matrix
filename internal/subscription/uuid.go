package subscription

import (
	"crypto/sha1"
	"fmt"
)

// slotUUID is stable for a given subscription token and slot id, so a client
// keeps its credentials across gateway restarts.
func slotUUID(token string, slotID int) string {
	sum := sha1.Sum([]byte(fmt.Sprintf("proxygate-sub-v1:%s:%d", token, slotID)))
	bytes := sum[:16]
	bytes[6] = (bytes[6] & 0x0f) | 0x50
	bytes[8] = (bytes[8] & 0x3f) | 0x80
	return uuidString(bytes)
}

// SlotPassword returns the stable trojan password for a subscription token and slot.
func SlotPassword(token string, slotID int) string { return slotUUID(token, slotID) }

func uuidString(raw []byte) string {
	if len(raw) < 16 {
		return ""
	}
	return fmt.Sprintf("%x-%x-%x-%x-%x", raw[0:4], raw[4:6], raw[6:8], raw[8:10], raw[10:16])
}
