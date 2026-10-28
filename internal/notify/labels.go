package notify

// labels are the words of cards and their answers, per language.
type labels struct {
	Request, Item, Deployment, Project string
	Open, Approve, Reject, Comment     string
	Approved, Rejected                 string // "Approved by %s"
	NeedComment, Decided, NotYours     string
	Failed                             string // "Not decided: %s"
}

var allLabels = map[string]labels{
	"en": {Request: "Request", Item: "Item", Deployment: "Deployment", Project: "Project", Open: "Open in rvav",
		Approve: "Approve", Reject: "Reject", Comment: "Comment (needed to reject)", Approved: "Approved by %s",
		Rejected: "Rejected by %s", NeedComment: "Say why you reject it: the requester sees your comment.",
		Decided: "This approval was already decided.", NotYours: "This card was sent to someone else.",
		Failed: "Not decided: %s"},
	"fr": {Request: "Demande", Item: "Élément", Deployment: "Déploiement", Project: "Projet", Open: "Ouvrir dans rvav",
		Approve: "Approuver", Reject: "Refuser", Comment: "Commentaire (requis pour refuser)", Approved: "Approuvée par %s",
		Rejected: "Refusée par %s", NeedComment: "Dites pourquoi vous refusez : le demandeur voit votre commentaire.",
		Decided: "Cette approbation a déjà été décidée.", NotYours: "Cette carte a été envoyée à quelqu'un d'autre.",
		Failed: "Non décidée : %s"},
}

func labelsFor(locale string) labels {
	if l, ok := allLabels[locale]; ok {
		return l
	}
	return allLabels["en"]
}
