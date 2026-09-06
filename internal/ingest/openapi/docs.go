package openapi

import (
	"strings"

	v3 "github.com/pb33f/libopenapi/datamodel/high/v3"

	"github.com/growsimplee/sapien/internal/domain"
)

// buildDocs extracts the contract-embedded documentation PLAN.md §5 calls out:
// info.description -> "contract#info", and each tag with a description ->
// "contract#tag:<name>".
func (b *builder) buildDocs(highDoc *v3.Document) []domain.Doc {
	var out []domain.Doc

	if highDoc.Info != nil && strings.TrimSpace(highDoc.Info.Description) != "" {
		title := highDoc.Info.Title
		if title == "" {
			title = b.serviceID
		}
		out = append(out, b.buildDoc("contract#info", title, highDoc.Info.Description, domain.DocSourceContractInfo))
	}

	for _, tag := range highDoc.Tags {
		if tag == nil || strings.TrimSpace(tag.Description) == "" {
			continue
		}
		out = append(out, b.buildDoc("contract#tag:"+tag.Name, tag.Name, tag.Description, domain.DocSourceContractTag))
	}

	return out
}

func (b *builder) buildDoc(path, title, body string, source domain.DocSource) domain.Doc {
	if b.docParser != nil {
		return b.docParser(b.serviceID, path, title, body, source)
	}
	docID := b.serviceID + "/" + path
	return domain.Doc{
		ID:        docID,
		ServiceID: b.serviceID,
		Path:      path,
		Title:     title,
		Source:    source,
		Hash:      hashText(body),
		Sections: []domain.DocSection{
			{
				ID:      docID + "#" + slug(title),
				Ord:     0,
				Heading: title,
				Level:   1,
				Body:    body,
			},
		},
	}
}

// slug lower-cases s and replaces runs of non [a-z0-9] characters with a single "-".
func slug(s string) string {
	var b strings.Builder
	prevDash := true // avoid a leading dash
	for _, r := range strings.ToLower(s) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevDash = false
			continue
		}
		if !prevDash {
			b.WriteByte('-')
			prevDash = true
		}
	}
	out := strings.TrimSuffix(b.String(), "-")
	if out == "" {
		out = "doc"
	}
	return out
}
