package notify

import (
	"fmt"
	"strings"
)

// labels are the words of cards and their answers, per language.
type labels struct {
	Request, Item, Deployment, Project string
	Open                               string // "Open in %s", the portal's name
	Approve, Reject, Comment           string
	Approved, Rejected                 string // "Approved by %s"
	NeedComment, Decided, NotYours     string
	Failed                             string // "Not decided: %s"
	// Buttons are an email's button per kind, Open for the others.
	Buttons map[string]string
}

var allLabels = map[string]labels{
	"en": {Request: "Request", Item: "Item", Deployment: "Deployment", Project: "Project", Open: "Open in %s",
		Approve: "Approve", Reject: "Reject", Comment: "Comment (needed to reject)", Approved: "Approved by %s",
		Rejected: "Rejected by %s", NeedComment: "Say why you reject it: the requester sees your comment.",
		Decided: "This approval was already decided.", NotYours: "This card was sent to someone else.",
		Failed: "Not decided: %s",
		Buttons: map[string]string{"approval_needed": "Decide in %s", "lease_expiring": "Extend the lease",
			"test": "Your notification preferences"}},
	"fr": {Request: "Demande", Item: "Élément", Deployment: "Déploiement", Project: "Projet", Open: "Ouvrir dans %s",
		Approve: "Approuver", Reject: "Refuser", Comment: "Commentaire (requis pour refuser)", Approved: "Approuvée par %s",
		Rejected: "Refusée par %s", NeedComment: "Dites pourquoi vous refusez : le demandeur voit votre commentaire.",
		Decided: "Cette approbation a déjà été décidée.", NotYours: "Cette carte a été envoyée à quelqu'un d'autre.",
		Failed: "Non décidée : %s",
		Buttons: map[string]string{"approval_needed": "Décider dans %s", "lease_expiring": "Prolonger le bail",
			"test": "Vos préférences de notification"}},
}

func labelsFor(locale string) labels {
	if l, ok := allLabels[locale]; ok {
		return l
	}
	return allLabels["en"]
}

// DefaultPortal is the portal's name until one is set.
const DefaultPortal = "rvav"

func portalOr(name string) string {
	if name == "" {
		return DefaultPortal
	}
	return name
}

// open is the label of the button that opens rvav.
func (l labels) open(portal string) string { return fmt.Sprintf(l.Open, portalOr(portal)) }

// button is an email's button for a kind.
func (l labels) button(kind, portal string) string {
	b, ok := l.Buttons[kind]
	if !ok {
		b = l.Open
	}
	if strings.Contains(b, "%s") {
		return fmt.Sprintf(b, portalOr(portal))
	}
	return b
}
