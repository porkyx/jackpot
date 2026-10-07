package desktop

import (
	"context"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/porkyx/jackpot/internal/contracts"
	rl "github.com/porkyx/jackpot/internal/roundlifecycle"
)

func frozenExpected(id contracts.CollectionID, revision *contracts.Revision) error {
	if id == "" || (revision != nil && contracts.ValidateCounter(*revision) != nil) {
		return contracts.NewFault(contracts.InvalidInput)
	}
	return nil
}
func frozenRevision(actual contracts.Revision, expected *contracts.Revision) error {
	if expected != nil && actual != *expected {
		return contracts.NewFault(contracts.StaleRevision)
	}
	return nil
}
func frozenParticipantProjection(participant rl.ParticipantSnapshot) (contracts.ParticipantData, error) {
	if participant.ID == "" || !utf8.ValidString(participant.Nickname) || !utf8.ValidString(participant.PublicIdentifier) || (participant.Classification != "unclassified" && participant.Classification != "included" && participant.Classification != "excluded") {
		return contracts.ParticipantData{}, contracts.NewFault(contracts.InvalidState)
	}
	texts := make([]string, 0, min(len(participant.Comments), contracts.MaxParticipantPreviews))
	for _, comment := range participant.Comments[:min(len(participant.Comments), contracts.MaxParticipantPreviews)] {
		text := comment.Text
		if text == "" && comment.Kind == "dccon" {
			text = "[디시콘]"
		}
		if text == "" && comment.Kind == "voice" {
			text = "[보플]"
		}
		texts = append(texts, text)
	}
	previews, err := contracts.PreviewTexts(texts)
	if err != nil {
		return contracts.ParticipantData{}, err
	}
	category, err := contracts.ResolveBadgeCategory(participant.BadgeCategory, participant.Kind)
	if err != nil {
		return contracts.ParticipantData{}, contracts.NewFault(contracts.InvalidState)
	}
	return contracts.ParticipantData{BadgeCategory: category, ID: participant.ID, Nickname: participant.Nickname, PublicIdentifier: participant.PublicIdentifier, Kind: participant.Kind, Classification: participant.Classification, Reason: participant.Reason, Included: participant.Included, CommentCount: uint32(len(participant.Comments)), Previews: previews}, nil
}
func frozenCommentProjection(comment rl.CommentSnapshot) (contracts.CommentData, error) {
	if comment.ID == "" || !utf8.ValidString(comment.Text) || (comment.Kind != "text" && comment.Kind != "dccon" && comment.Kind != "voice") {
		return contracts.CommentData{}, contracts.NewFault(contracts.InvalidState)
	}
	data := contracts.CommentData{ID: comment.ID, Kind: comment.Kind, Text: comment.Text, MediaURLs: make([]string, 0, len(comment.MediaURLs))}
	if comment.ParentID != nil {
		if *comment.ParentID == "" {
			return contracts.CommentData{}, contracts.NewFault(contracts.InvalidState)
		}
		parent := *comment.ParentID
		data.ParentID = &parent
	}
	if comment.PostedAt != nil {
		stamp := *comment.PostedAt
		if _, err := contracts.NewResponseHeader("comment", stamp); err != nil {
			return contracts.CommentData{}, contracts.NewFault(contracts.InvalidState)
		}
		data.PostedAt = &stamp
	}
	for _, raw := range comment.MediaURLs {
		media, err := url.Parse(raw)
		if err != nil || media.Scheme != "https" || media.User != nil || media.Port() != "" {
			return contracts.CommentData{}, contracts.NewFault(contracts.InvalidState)
		}
		switch strings.ToLower(media.Hostname()) {
		case "dcimg5.dcinside.com", "dcimg4.dcinside.com", "dcimg1.dcinside.com", "dccon.dcinside.com", "image.dcinside.com":
		default:
			return contracts.CommentData{}, contracts.NewFault(contracts.InvalidState)
		}
		data.MediaURLs = append(data.MediaURLs, raw)
	}
	return data, nil
}
func (service *RoundService) QueryFrozenParticipants(ctx context.Context, query contracts.FrozenParticipantsQuery) (contracts.FrozenParticipantsResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.FrozenParticipantsResponse{}, err
	}
	fail := func(err error) (contracts.FrozenParticipantsResponse, error) {
		return contracts.FrozenParticipantsResponse(failureEnvelope[contracts.FrozenParticipantsPage](header, safeProductError(err))), nil
	}
	if frozenExpected(query.CollectionID, query.ExpectedRevision) != nil || query.Limit < 1 || query.Limit > 100 || len(query.Query) > 1000 || !utf8.ValidString(query.Query) || (query.Group != "" && query.Group != "unclassified" && query.Group != "included" && query.Group != "excluded") {
		return fail(contracts.NewFault(contracts.InvalidInput))
	}
	frozen, revision, err := service.reader.ReadCollectionSnapshot(ctx, query.CollectionID)
	if err != nil {
		return fail(err)
	}
	if err = frozenRevision(revision, query.ExpectedRevision); err != nil {
		return fail(err)
	}
	page := contracts.FrozenParticipantsPage{CollectionID: query.CollectionID, Revision: revision, Offset: query.Offset, Rows: make([]contracts.ParticipantData, 0, query.Limit)}
	needle := strings.ToLower(query.Query)
	for _, participant := range frozen.Participants {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if query.Group != "" && participant.Classification != query.Group {
			continue
		}
		page.Total++
		if !strings.Contains(strings.ToLower(participant.Nickname), needle) && !strings.Contains(strings.ToLower(participant.PublicIdentifier), needle) {
			continue
		}
		index := page.Matched
		page.Matched++
		if index < query.Offset || uint32(len(page.Rows)) >= query.Limit {
			continue
		}
		row, err := frozenParticipantProjection(participant)
		if err != nil {
			return fail(err)
		}
		page.Rows = append(page.Rows, row)
	}
	response := contracts.FrozenParticipantsResponse{ResponseHeader: header, OK: true, Data: &page}
	checked, checkErr := checkedQuery(contracts.Envelope[contracts.FrozenParticipantsPage](response))
	return contracts.FrozenParticipantsResponse(checked), checkErr
}
func (service *RoundService) QueryFrozenComments(ctx context.Context, query contracts.FrozenCommentsQuery) (contracts.FrozenCommentsResponse, error) {
	header, err := service.header(ctx)
	if err != nil {
		return contracts.FrozenCommentsResponse{}, err
	}
	fail := func(err error) (contracts.FrozenCommentsResponse, error) {
		return contracts.FrozenCommentsResponse(failureEnvelope[contracts.FrozenCommentsPage](header, safeProductError(err))), nil
	}
	if frozenExpected(query.CollectionID, query.ExpectedRevision) != nil || query.ParticipantID == "" || query.Limit < 1 || query.Limit > 50 {
		return fail(contracts.NewFault(contracts.InvalidInput))
	}
	frozen, revision, err := service.reader.ReadCollectionSnapshot(ctx, query.CollectionID)
	if err != nil {
		return fail(err)
	}
	if err = frozenRevision(revision, query.ExpectedRevision); err != nil {
		return fail(err)
	}
	for _, participant := range frozen.Participants {
		if err = ctx.Err(); err != nil {
			return fail(err)
		}
		if participant.ID != query.ParticipantID {
			continue
		}
		page := contracts.FrozenCommentsPage{CollectionID: query.CollectionID, Revision: revision, ParticipantID: participant.ID, Total: uint32(len(participant.Comments)), Offset: query.Offset, Rows: make([]contracts.CommentData, 0, query.Limit)}
		for index, comment := range participant.Comments {
			if err = ctx.Err(); err != nil {
				return fail(err)
			}
			if uint32(index) < query.Offset || uint32(len(page.Rows)) >= query.Limit {
				continue
			}
			data, err := frozenCommentProjection(comment)
			if err != nil {
				return fail(err)
			}
			page.Rows = append(page.Rows, data)
		}
		response := contracts.FrozenCommentsResponse{ResponseHeader: header, OK: true, Data: &page}
		checked, checkErr := checkedQuery(contracts.Envelope[contracts.FrozenCommentsPage](response))
		return contracts.FrozenCommentsResponse(checked), checkErr
	}
	return fail(contracts.NewFault(contracts.InvalidInput))
}
