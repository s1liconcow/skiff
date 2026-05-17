package auth

import (
	"strings"

	"github.com/s1liconcow/skiff/internal/state/schema"
)

const (
	ActorUser       = "user"
	ActorCI         = "ci"
	ActorAgent      = "agent"
	ActorSkiffd     = "skiffd"
	ActorWorker     = "worker"
	ActorBreakGlass = "break-glass"
)

func Normalize(actor schema.Actor) schema.Actor {
	actor.ID = strings.TrimSpace(actor.ID)
	actor.Type = strings.TrimSpace(strings.ToLower(actor.Type))
	if actor.Type == "" {
		actor.Type = ActorUser
	}
	if actor.ID == "" {
		switch actor.Type {
		case ActorCI:
			actor.ID = "ci"
		case ActorAgent:
			actor.ID = "agent"
		case ActorSkiffd:
			actor.ID = "skiffd"
		case ActorWorker:
			actor.ID = "worker"
		case ActorBreakGlass:
			actor.ID = "break-glass"
		default:
			actor.ID = "user"
		}
	}
	return actor
}

func IsBreakGlass(actor schema.Actor) bool {
	actor = Normalize(actor)
	return actor.Type == ActorBreakGlass || strings.HasPrefix(actor.ID, "break-glass:")
}

func Roles(actor schema.Actor) []string {
	actor = Normalize(actor)
	switch actor.Type {
	case ActorCI:
		return []string{"reader", "deployer", "release-manager"}
	case ActorAgent:
		return []string{"reader", "planner", "agent"}
	case ActorSkiffd:
		return []string{"reader", "deployer", "operator", "approver", "system"}
	case ActorWorker:
		return []string{"reader", "worker"}
	case ActorBreakGlass:
		return []string{"reader", "operator", "approver", "security-admin", "database-admin", "platform-admin", "break-glass"}
	default:
		return []string{"reader", "planner", "operator", "approver"}
	}
}

func HasRole(actor schema.Actor, role string) bool {
	role = strings.TrimSpace(role)
	if role == "" {
		return true
	}
	for _, item := range Roles(actor) {
		if item == role {
			return true
		}
	}
	return false
}
