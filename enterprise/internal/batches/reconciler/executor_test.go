package reconciler

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sourcegraph/log/logtest"

	"github.com/sourcegraph/sourcegraph/enterprise/internal/batches/sources"
	stesting "github.com/sourcegraph/sourcegraph/enterprise/internal/batches/sources/testing"
	"github.com/sourcegraph/sourcegraph/enterprise/internal/batches/store"
	bt "github.com/sourcegraph/sourcegraph/enterprise/internal/batches/testing"
	btypes "github.com/sourcegraph/sourcegraph/enterprise/internal/batches/types"
	"github.com/sourcegraph/sourcegraph/internal/actor"
	"github.com/sourcegraph/sourcegraph/internal/api/internalapi"
	"github.com/sourcegraph/sourcegraph/internal/database"
	"github.com/sourcegraph/sourcegraph/internal/database/dbtest"
	et "github.com/sourcegraph/sourcegraph/internal/encryption/testing"
	"github.com/sourcegraph/sourcegraph/internal/errcode"
	"github.com/sourcegraph/sourcegraph/internal/extsvc/auth"
	"github.com/sourcegraph/sourcegraph/internal/gitserver/gitdomain"
	gitprotocol "github.com/sourcegraph/sourcegraph/internal/gitserver/protocol"
	"github.com/sourcegraph/sourcegraph/internal/observation"
	"github.com/sourcegraph/sourcegraph/internal/repos"
	"github.com/sourcegraph/sourcegraph/internal/repoupdater/protocol"
	"github.com/sourcegraph/sourcegraph/internal/timeutil"
	"github.com/sourcegraph/sourcegraph/internal/types"
	"github.com/sourcegraph/sourcegraph/lib/batches/git"
	"github.com/sourcegraph/sourcegraph/lib/errors"
)

func TestExecutor_ExecutePlan(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))

	now := timeutil.Now()
	clock := func() time.Time { return now }
	bstore := store.NewWithClock(db, &observation.TestContext, et.TestKey{}, clock)

	admin := bt.CreateTestUser(t, db, true)
	ctx = actor.WithActor(ctx, actor.FromUser(admin.ID))

	repo, extSvc := bt.CreateTestRepo(t, ctx, db)
	bt.CreateTestSiteCredential(t, bstore, repo)

	state := bt.MockChangesetSyncState(&protocol.RepoInfo{
		Name: repo.Name,
		VCS:  protocol.VCSInfo{URL: repo.URI},
	})
	defer state.Unmock()

	internalClient = &mockInternalClient{externalURL: "https://sourcegraph.test"}
	defer func() { internalClient = internalapi.Client }()

	githubPR := buildGithubPR(clock(), btypes.ChangesetExternalStateOpen)
	githubHeadRef := gitdomain.EnsureRefPrefix(githubPR.HeadRefName)
	draftGithubPR := buildGithubPR(clock(), btypes.ChangesetExternalStateDraft)
	closedGitHubPR := buildGithubPR(clock(), btypes.ChangesetExternalStateClosed)

	notFoundErr := sources.ChangesetNotFoundError{
		Changeset: &sources.Changeset{
			Changeset: &btypes.Changeset{ExternalID: "100000"},
		},
	}

	repoArchivedErr := mockRepoArchivedError{}

	type testCase struct {
		changeset      bt.TestChangesetOpts
		hasCurrentSpec bool
		plan           *Plan

		sourcerMetadata any
		sourcerErr      error
		// Whether or not the source responds to CreateChangeset with "already exists"
		alreadyExists bool
		// Whether or not the source responds to IsArchivedPushError with true
		isRepoArchived bool

		gitClientErr error

		wantCreateOnCodeHost      bool
		wantCreateDraftOnCodeHost bool
		wantUndraftOnCodeHost     bool
		wantUpdateOnCodeHost      bool
		wantCloseOnCodeHost       bool
		wantLoadFromCodeHost      bool
		wantReopenOnCodeHost      bool

		wantGitserverCommit bool

		wantChangeset       bt.ChangesetAssertions
		wantNonRetryableErr bool
	}

	tests := map[string]testCase{
		"noop": {
			hasCurrentSpec: true,
			changeset:      bt.TestChangesetOpts{},
			plan:           &Plan{Ops: Operations{}},

			wantChangeset: bt.ChangesetAssertions{},
		},
		"import": {
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
				ExternalID:       githubPR.ID,
			},
			plan: &Plan{
				Ops: Operations{btypes.ReconcilerOperationImport},
			},

			wantLoadFromCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				Title:            githubPR.Title,
				Body:             githubPR.Body,
				DiffStat:         state.DiffStat,
			},
		},
		"import and not-found error": {
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
				ExternalID:       githubPR.ID,
			},
			plan: &Plan{
				Ops: Operations{btypes.ReconcilerOperationImport},
			},
			sourcerErr: notFoundErr,

			wantLoadFromCodeHost: true,

			wantNonRetryableErr: true,
		},
		"push and publish": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationPublish,
				},
			},

			wantCreateOnCodeHost: true,
			wantGitserverCommit:  true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				Title:            githubPR.Title,
				Body:             githubPR.Body,
				DiffStat:         state.DiffStat,
			},
		},
		"retry push and publish": {
			// This test case makes sure that everything works when the code host says
			// that the changeset already exists.
			alreadyExists:  true,
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				// The reconciler resets the failure message before passing the
				// changeset to the executor.
				// We simulate that here by not setting FailureMessage.
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationPublish,
				},
			},

			// We first do a create and since that fails with "already exists"
			// we update.
			wantCreateOnCodeHost: true,
			wantUpdateOnCodeHost: true,
			wantGitserverCommit:  true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				Title:            githubPR.Title,
				Body:             githubPR.Body,
				DiffStat:         state.DiffStat,
			},
		},
		"push and publish to archived repo, detected at push": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationPublish,
				},
			},
			gitClientErr: &gitprotocol.CreateCommitFromPatchError{
				CombinedOutput: "archived",
			},
			isRepoArchived: true,
			sourcerErr:     repoArchivedErr,

			wantGitserverCommit: true,
			wantNonRetryableErr: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
		},
		"push and publish to archived repo, detected at publish": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationPublish,
				},
			},
			sourcerErr: repoArchivedErr,

			wantCreateOnCodeHost: true,
			wantGitserverCommit:  true,
			wantNonRetryableErr:  true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},
		},
		"update": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       "12345",
				ExternalBranch:   "head-ref-on-github",
			},

			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationUpdate,
				},
			},

			// We don't want a new commit, only an update on the code host.
			wantUpdateOnCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				DiffStat:         state.DiffStat,
				// We update the title/body but want the title/body returned by the code host.
				Title: githubPR.Title,
				Body:  githubPR.Body,
			},
		},
		"update to archived repo": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       "12345",
				ExternalBranch:   "head-ref-on-github",
			},

			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationUpdate,
				},
			},
			sourcerErr: repoArchivedErr,

			// We don't want a new commit, only an update on the code host.
			wantUpdateOnCodeHost: true,
			wantNonRetryableErr:  true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateReadOnly,
				DiffStat:         state.DiffStat,
				// We update the title/body but want the title/body returned by the code host.
				Title: githubPR.Title,
				Body:  githubPR.Body,
			},
		},
		"push sleep sync": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       "12345",
				ExternalBranch:   gitdomain.EnsureRefPrefix("head-ref-on-github"),
				ExternalState:    btypes.ChangesetExternalStateOpen,
			},

			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationSleep,
					btypes.ReconcilerOperationSync,
				},
			},

			wantGitserverCommit:  true,
			wantLoadFromCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				DiffStat:         state.DiffStat,
			},
		},
		"close open changeset": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				Closing:          true,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationClose,
				},
			},
			// We return a closed GitHub PR here
			sourcerMetadata: closedGitHubPR,

			wantCloseOnCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				Closing:          false,

				ExternalID:     closedGitHubPR.ID,
				ExternalBranch: gitdomain.EnsureRefPrefix(closedGitHubPR.HeadRefName),
				ExternalState:  btypes.ChangesetExternalStateClosed,

				Title:    closedGitHubPR.Title,
				Body:     closedGitHubPR.Body,
				DiffStat: state.DiffStat,
			},
		},
		"close closed changeset": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateClosed,
				Closing:          true,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationClose,
				},
			},

			// We return a closed GitHub PR here, but since it's a noop, we
			// don't sync and thus don't set its attributes on the changeset.
			sourcerMetadata: closedGitHubPR,

			// Should be a noop
			wantCloseOnCodeHost: false,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				Closing:          false,

				ExternalID:     closedGitHubPR.ID,
				ExternalBranch: gitdomain.EnsureRefPrefix(closedGitHubPR.HeadRefName),
				ExternalState:  btypes.ChangesetExternalStateClosed,
			},
		},
		"reopening closed changeset without updates": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateClosed,
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationReopen,
				},
			},

			wantReopenOnCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,

				ExternalID:     githubPR.ID,
				ExternalBranch: githubHeadRef,
				ExternalState:  btypes.ChangesetExternalStateOpen,

				Title:    githubPR.Title,
				Body:     githubPR.Body,
				DiffStat: state.DiffStat,
			},
		},
		"push and publishdraft": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStateUnpublished,
			},

			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationPush,
					btypes.ReconcilerOperationPublishDraft,
				},
			},

			sourcerMetadata: draftGithubPR,

			wantCreateDraftOnCodeHost: true,
			wantGitserverCommit:       true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,

				ExternalID:     draftGithubPR.ID,
				ExternalBranch: gitdomain.EnsureRefPrefix(draftGithubPR.HeadRefName),
				ExternalState:  btypes.ChangesetExternalStateDraft,

				Title:    draftGithubPR.Title,
				Body:     draftGithubPR.Body,
				DiffStat: state.DiffStat,
			},
		},
		"undraft": {
			hasCurrentSpec: true,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalState:    btypes.ChangesetExternalStateDraft,
			},

			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationUndraft,
				},
			},

			wantUndraftOnCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,

				ExternalID:     githubPR.ID,
				ExternalBranch: githubHeadRef,
				ExternalState:  btypes.ChangesetExternalStateOpen,

				Title:    githubPR.Title,
				Body:     githubPR.Body,
				DiffStat: state.DiffStat,
			},
		},
		"archive open changeset": {
			hasCurrentSpec: false,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateOpen,
				Closing:          true,
				BatchChanges: []btypes.BatchChangeAssoc{{
					BatchChangeID: 1234, Archive: true, IsArchived: false,
				}},
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationClose,
					btypes.ReconcilerOperationArchive,
				},
			},
			// We return a closed GitHub PR here
			sourcerMetadata: closedGitHubPR,

			wantCloseOnCodeHost: true,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				Closing:          false,

				ExternalID:     closedGitHubPR.ID,
				ExternalBranch: gitdomain.EnsureRefPrefix(closedGitHubPR.HeadRefName),
				ExternalState:  btypes.ChangesetExternalStateClosed,

				Title:    closedGitHubPR.Title,
				Body:     closedGitHubPR.Body,
				DiffStat: state.DiffStat,

				ArchivedInOwnerBatchChange: true,
			},
		},
		"detach changeset": {
			hasCurrentSpec: false,
			changeset: bt.TestChangesetOpts{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				ExternalID:       githubPR.ID,
				ExternalBranch:   githubHeadRef,
				ExternalState:    btypes.ChangesetExternalStateClosed,
				Closing:          false,
				BatchChanges: []btypes.BatchChangeAssoc{{
					BatchChangeID: 1234, Detach: true,
				}},
			},
			plan: &Plan{
				Ops: Operations{
					btypes.ReconcilerOperationDetach,
				},
			},

			wantCloseOnCodeHost: false,

			wantChangeset: bt.ChangesetAssertions{
				PublicationState: btypes.ChangesetPublicationStatePublished,
				Closing:          false,

				ExternalID:     closedGitHubPR.ID,
				ExternalBranch: git.EnsureRefPrefix(closedGitHubPR.HeadRefName),
				ExternalState:  btypes.ChangesetExternalStateClosed,

				ArchivedInOwnerBatchChange: false,
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			// Create necessary associations.
			batchSpec := bt.CreateBatchSpec(t, ctx, bstore, "executor-test-batch-change", admin.ID, 0)
			batchChange := bt.CreateBatchChange(t, ctx, bstore, "executor-test-batch-change", admin.ID, batchSpec.ID)

			// Create the changesetSpec with associations wired up correctly.
			var changesetSpec *btypes.ChangesetSpec
			if tc.hasCurrentSpec {
				// The attributes of the spec don't really matter, but the
				// associations do.
				specOpts := bt.TestSpecOpts{}
				specOpts.User = admin.ID
				specOpts.Repo = repo.ID
				specOpts.BatchSpec = batchSpec.ID
				changesetSpec = bt.CreateChangesetSpec(t, ctx, bstore, specOpts)
			}

			// Create the changeset with correct associations.
			changesetOpts := tc.changeset
			changesetOpts.Repo = repo.ID
			if len(changesetOpts.BatchChanges) != 0 {
				for i := range changesetOpts.BatchChanges {
					changesetOpts.BatchChanges[i].BatchChangeID = batchChange.ID
				}
			} else {
				changesetOpts.BatchChanges = []btypes.BatchChangeAssoc{{BatchChangeID: batchChange.ID}}
			}
			changesetOpts.OwnedByBatchChange = batchChange.ID
			if changesetSpec != nil {
				changesetOpts.CurrentSpec = changesetSpec.ID
			}
			changeset := bt.CreateChangeset(t, ctx, bstore, changesetOpts)

			// Setup gitserver dependency.
			gitClient := &bt.FakeGitserverClient{ResponseErr: tc.gitClientErr}
			if changesetSpec != nil {
				gitClient.Response = changesetSpec.Spec.HeadRef
			}

			// Setup the sourcer that's used to create a Source with which
			// to create/update a changeset.
			fakeSource := &stesting.FakeChangesetSource{
				Svc:                     extSvc,
				Err:                     tc.sourcerErr,
				ChangesetExists:         tc.alreadyExists,
				IsArchivedPushErrorTrue: tc.isRepoArchived,
			}

			if tc.sourcerMetadata != nil {
				fakeSource.FakeMetadata = tc.sourcerMetadata
			} else {
				fakeSource.FakeMetadata = githubPR
			}
			if changesetSpec != nil {
				fakeSource.WantHeadRef = changesetSpec.Spec.HeadRef
				fakeSource.WantBaseRef = changesetSpec.Spec.BaseRef
			}

			sourcer := stesting.NewFakeSourcer(nil, fakeSource)

			tc.plan.Changeset = changeset
			tc.plan.ChangesetSpec = changesetSpec

			// Ensure we reset the state of the repo after executing the plan.
			t.Cleanup(func() {
				repo.Archived = false
				_, err := repos.NewStore(logtest.Scoped(t), bstore.DatabaseDB()).UpdateRepo(ctx, repo)
				require.NoError(t, err)
			})

			// Execute the plan
			err := executePlan(
				ctx,
				logtest.Scoped(t),
				gitClient,
				sourcer,
				// Don't actually sleep for the sake of testing.
				true,
				bstore,
				tc.plan,
			)
			if err != nil {
				if tc.wantNonRetryableErr && errcode.IsNonRetryable(err) {
					// all good
				} else {
					t.Fatalf("ExecutePlan failed: %s", err)
				}
			}

			// Assert that all the calls happened
			if have, want := gitClient.CreateCommitFromPatchCalled, tc.wantGitserverCommit; have != want {
				t.Fatalf("wrong CreateCommitFromPatch call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.CreateDraftChangesetCalled, tc.wantCreateDraftOnCodeHost; have != want {
				t.Fatalf("wrong CreateDraftChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.UndraftedChangesetsCalled, tc.wantUndraftOnCodeHost; have != want {
				t.Fatalf("wrong UndraftChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.CreateChangesetCalled, tc.wantCreateOnCodeHost; have != want {
				t.Fatalf("wrong CreateChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.UpdateChangesetCalled, tc.wantUpdateOnCodeHost; have != want {
				t.Fatalf("wrong UpdateChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.ReopenChangesetCalled, tc.wantReopenOnCodeHost; have != want {
				t.Fatalf("wrong ReopenChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.LoadChangesetCalled, tc.wantLoadFromCodeHost; have != want {
				t.Fatalf("wrong LoadChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if have, want := fakeSource.CloseChangesetCalled, tc.wantCloseOnCodeHost; have != want {
				t.Fatalf("wrong CloseChangeset call. wantCalled=%t, wasCalled=%t", want, have)
			}

			if tc.wantNonRetryableErr {
				return
			}

			// Determine if a detach operation is being done
			hasDetachOperation := false
			for _, op := range tc.plan.Ops {
				if op == btypes.ReconcilerOperationDetach {
					hasDetachOperation = true
					break
				}
			}

			// Assert that the changeset in the database looks like we want
			assertions := tc.wantChangeset
			assertions.Repo = repo.ID
			assertions.OwnedByBatchChange = changesetOpts.OwnedByBatchChange
			// There are no AttachedTo for detach operations
			if !hasDetachOperation {
				assertions.AttachedTo = []int64{batchChange.ID}
			}
			if changesetSpec != nil {
				assertions.CurrentSpec = changesetSpec.ID
			}
			bt.ReloadAndAssertChangeset(t, ctx, bstore, changeset, assertions)

			// Assert that the body included a backlink if needed. We'll do
			// more detailed unit tests of decorateChangesetBody elsewhere;
			// we're just looking for a basic marker here that _something_
			// happened.
			var rcs *sources.Changeset
			if tc.wantCreateOnCodeHost && fakeSource.CreateChangesetCalled {
				rcs = fakeSource.CreatedChangesets[0]
			} else if tc.wantUpdateOnCodeHost && fakeSource.UpdateChangesetCalled {
				rcs = fakeSource.UpdatedChangesets[0]
			}

			if rcs != nil {
				if !strings.Contains(rcs.Body, "Created by Sourcegraph batch change") {
					t.Errorf("did not find backlink in body: %q", rcs.Body)
				}
			}

			// Ensure the detached_at timestamp is set when the operation is detach
			if hasDetachOperation {
				assert.NotNil(t, changeset.DetachedAt)
			}
		})

		// After each test: clean up database.
		bt.TruncateTables(t, db, "changeset_events", "changesets", "batch_changes", "batch_specs", "changeset_specs")
	}
}

func TestExecutor_ExecutePlan_PublishedChangesetDuplicateBranch(t *testing.T) {
	if testing.Short() {
		t.Skip()
	}

	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))

	bstore := store.New(db, &observation.TestContext, et.TestKey{})

	repo, _ := bt.CreateTestRepo(t, ctx, db)

	commonHeadRef := "refs/heads/collision"

	// Create a published changeset.
	bt.CreateChangeset(t, ctx, bstore, bt.TestChangesetOpts{
		Repo:             repo.ID,
		PublicationState: btypes.ChangesetPublicationStatePublished,
		ExternalBranch:   commonHeadRef,
		ExternalID:       "123",
	})

	// Plan only needs a push operation, since that's where we check
	plan := &Plan{}
	plan.AddOp(btypes.ReconcilerOperationPush)

	// Build a changeset that would be pushed on the same HeadRef/ExternalBranch.
	plan.ChangesetSpec = bt.BuildChangesetSpec(t, bt.TestSpecOpts{
		Repo:      repo.ID,
		HeadRef:   commonHeadRef,
		Published: true,
	})
	plan.Changeset = bt.BuildChangeset(bt.TestChangesetOpts{Repo: repo.ID})

	err := executePlan(ctx, logtest.Scoped(t), nil, stesting.NewFakeSourcer(nil, &stesting.FakeChangesetSource{}), true, bstore, plan)
	if err == nil {
		t.Fatal("reconciler did not return error")
	}

	// We expect a non-retryable error to be returned.
	if !errcode.IsNonRetryable(err) {
		t.Fatalf("error is not non-retryabe. have=%s", err)
	}
}

func TestExecutor_ExecutePlan_AvoidLoadingChangesetSource(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := context.Background()
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	bstore := store.New(db, &observation.TestContext, et.TestKey{})
	repo, _ := bt.CreateTestRepo(t, ctx, db)

	changesetSpec := bt.BuildChangesetSpec(t, bt.TestSpecOpts{
		Repo:      repo.ID,
		HeadRef:   "refs/heads/my-pr",
		Published: true,
	})
	changeset := bt.BuildChangeset(bt.TestChangesetOpts{ExternalState: "OPEN", Repo: repo.ID})

	ourError := errors.New("this should not be returned")
	sourcer := stesting.NewFakeSourcer(ourError, &stesting.FakeChangesetSource{})

	t.Run("plan requires changeset source", func(t *testing.T) {
		plan := &Plan{}
		plan.ChangesetSpec = changesetSpec
		plan.Changeset = changeset

		plan.AddOp(btypes.ReconcilerOperationClose)

		err := executePlan(ctx, logtest.Scoped(t), nil, sourcer, true, bstore, plan)
		if err != ourError {
			t.Fatalf("executePlan did not return expected error: %s", err)
		}
	})

	t.Run("plan does not require changeset source", func(t *testing.T) {
		plan := &Plan{}
		plan.ChangesetSpec = changesetSpec
		plan.Changeset = changeset

		plan.AddOp(btypes.ReconcilerOperationDetach)

		err := executePlan(ctx, logtest.Scoped(t), nil, sourcer, true, bstore, plan)
		if err != nil {
			t.Fatalf("executePlan returned unexpected error: %s", err)
		}
	})
}

func TestLoadChangesetSource(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := actor.WithInternalActor(context.Background())
	db := database.NewDB(logger, dbtest.NewDB(logger, t))
	token := &auth.OAuthBearerToken{Token: "abcdef"}

	bstore := store.New(db, &observation.TestContext, et.TestKey{})

	admin := bt.CreateTestUser(t, db, true)
	user := bt.CreateTestUser(t, db, false)

	repo, _ := bt.CreateTestRepo(t, ctx, db)

	batchSpec := bt.CreateBatchSpec(t, ctx, bstore, "reconciler-test-batch-change", admin.ID, 0)
	adminBatchChange := bt.CreateBatchChange(t, ctx, bstore, "reconciler-test-batch-change", admin.ID, batchSpec.ID)
	userBatchChange := bt.CreateBatchChange(t, ctx, bstore, "reconciler-test-batch-change", user.ID, batchSpec.ID)

	t.Run("imported changeset uses global token when no site-credential exists", func(t *testing.T) {
		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: 0,
		}, repo)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if fakeSource.CurrentAuthenticator != nil {
			t.Errorf("unexpected non-nil authenticator: %v", fakeSource.CurrentAuthenticator)
		}
	})

	t.Run("imported changeset uses site-credential when exists", func(t *testing.T) {
		if err := bstore.CreateSiteCredential(ctx, &btypes.SiteCredential{
			ExternalServiceType: repo.ExternalRepo.ServiceType,
			ExternalServiceID:   repo.ExternalRepo.ServiceID,
		}, token); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			bt.TruncateTables(t, db, "batch_changes_site_credentials")
		})
		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: 0,
		}, repo)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if diff := cmp.Diff(token, fakeSource.CurrentAuthenticator); diff != "" {
			t.Errorf("unexpected authenticator:\n%s", diff)
		}
	})

	t.Run("owned by missing batch change", func(t *testing.T) {
		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: 1234,
		}, repo)
		if err == nil {
			t.Error("unexpected nil error")
		}
	})

	t.Run("owned by admin user without credential", func(t *testing.T) {
		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: adminBatchChange.ID,
		}, repo)
		if !errors.Is(err, errMissingCredentials{repo: string(repo.Name)}) {
			t.Fatalf("unexpected error %v", err)
		}
	})

	t.Run("owned by normal user without credential", func(t *testing.T) {
		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: userBatchChange.ID,
		}, repo)
		if err == nil {
			t.Error("unexpected nil error")
		}
	})

	t.Run("owned by admin user with credential", func(t *testing.T) {
		if _, err := bstore.UserCredentials().Create(ctx, database.UserCredentialScope{
			Domain:              database.UserCredentialDomainBatches,
			UserID:              admin.ID,
			ExternalServiceType: repo.ExternalRepo.ServiceType,
			ExternalServiceID:   repo.ExternalRepo.ServiceID,
		}, token); err != nil {
			t.Fatal(err)
		}

		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: adminBatchChange.ID,
		}, repo)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if diff := cmp.Diff(token, fakeSource.CurrentAuthenticator); diff != "" {
			t.Errorf("unexpected authenticator:\n%s", diff)
		}
	})

	t.Run("owned by normal user with credential", func(t *testing.T) {
		if _, err := bstore.UserCredentials().Create(ctx, database.UserCredentialScope{
			Domain:              database.UserCredentialDomainBatches,
			UserID:              user.ID,
			ExternalServiceType: repo.ExternalRepo.ServiceType,
			ExternalServiceID:   repo.ExternalRepo.ServiceID,
		}, token); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			bt.TruncateTables(t, db, "user_credentials")
		})

		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: userBatchChange.ID,
		}, repo)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if diff := cmp.Diff(token, fakeSource.CurrentAuthenticator); diff != "" {
			t.Errorf("unexpected authenticator:\n%s", diff)
		}
	})

	t.Run("owned by user without credential falls back to site-credential", func(t *testing.T) {
		if err := bstore.CreateSiteCredential(ctx, &btypes.SiteCredential{
			ExternalServiceType: repo.ExternalRepo.ServiceType,
			ExternalServiceID:   repo.ExternalRepo.ServiceID,
		}, token); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			bt.TruncateTables(t, db, "batch_changes_site_credentials")
		})

		fakeSource := &stesting.FakeChangesetSource{}
		sourcer := stesting.NewFakeSourcer(nil, fakeSource)
		_, err := loadChangesetSource(ctx, bstore, sourcer, &btypes.Changeset{
			OwnedByBatchChangeID: userBatchChange.ID,
		}, repo)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if diff := cmp.Diff(token, fakeSource.CurrentAuthenticator); diff != "" {
			t.Errorf("unexpected authenticator:\n%s", diff)
		}
	})
}

func TestExecutor_UserCredentialsForGitserver(t *testing.T) {
	logger := logtest.Scoped(t)
	ctx := actor.WithInternalActor(context.Background())
	db := database.NewDB(logger, dbtest.NewDB(logger, t))

	bstore := store.New(db, &observation.TestContext, et.TestKey{})

	admin := bt.CreateTestUser(t, db, true)
	user := bt.CreateTestUser(t, db, false)

	gitHubRepo, gitHubExtSvc := bt.CreateTestRepo(t, ctx, db)

	gitLabRepos, gitLabExtSvc := bt.CreateGitlabTestRepos(t, ctx, db, 1)
	gitLabRepo := gitLabRepos[0]

	bbsRepos, bbsExtSvc := bt.CreateBbsTestRepos(t, ctx, db, 1)
	bbsRepo := bbsRepos[0]

	bbsSSHRepos, bbsSSHExtsvc := bt.CreateBbsSSHTestRepos(t, ctx, db, 1)
	bbsSSHRepo := bbsSSHRepos[0]

	plan := &Plan{}
	plan.AddOp(btypes.ReconcilerOperationPush)

	tests := []struct {
		name           string
		user           *types.User
		extSvc         *types.ExternalService
		repo           *types.Repo
		credentials    auth.Authenticator
		wantErr        bool
		wantPushConfig *gitprotocol.PushConfig
	}{
		{
			name:        "github OAuthBearerToken",
			user:        user,
			extSvc:      gitHubExtSvc,
			repo:        gitHubRepo,
			credentials: &auth.OAuthBearerToken{Token: "my-secret-github-token"},
			wantPushConfig: &gitprotocol.PushConfig{
				RemoteURL: "https://my-secret-github-token@github.com/sourcegraph/" + string(gitHubRepo.Name),
			},
		},
		{
			name:    "github no credentials",
			user:    user,
			extSvc:  gitHubExtSvc,
			repo:    gitHubRepo,
			wantErr: true,
		},
		{
			name:    "github site-admin and no credentials",
			extSvc:  gitHubExtSvc,
			repo:    gitHubRepo,
			user:    admin,
			wantErr: true,
		},
		{
			name:        "gitlab OAuthBearerToken",
			user:        user,
			extSvc:      gitLabExtSvc,
			repo:        gitLabRepo,
			credentials: &auth.OAuthBearerToken{Token: "my-secret-gitlab-token"},
			wantPushConfig: &gitprotocol.PushConfig{
				RemoteURL: "https://git:my-secret-gitlab-token@gitlab.com/sourcegraph/" + string(gitLabRepo.Name),
			},
		},
		{
			name:    "gitlab no credentials",
			user:    user,
			extSvc:  gitLabExtSvc,
			repo:    gitLabRepo,
			wantErr: true,
		},
		{
			name:    "gitlab site-admin and no credentials",
			user:    admin,
			extSvc:  gitLabExtSvc,
			repo:    gitLabRepo,
			wantErr: true,
		},
		{
			name:        "bitbucketServer BasicAuth",
			user:        user,
			extSvc:      bbsExtSvc,
			repo:        bbsRepo,
			credentials: &auth.BasicAuth{Username: "fredwoard johnssen", Password: "my-secret-bbs-token"},
			wantPushConfig: &gitprotocol.PushConfig{
				RemoteURL: "https://fredwoard%20johnssen:my-secret-bbs-token@bitbucket.sourcegraph.com/scm/" + string(bbsRepo.Name),
			},
		},
		{
			name:    "bitbucketServer no credentials",
			user:    user,
			extSvc:  bbsExtSvc,
			repo:    bbsRepo,
			wantErr: true,
		},
		{
			name:    "bitbucketServer site-admin and no credentials",
			user:    admin,
			extSvc:  bbsExtSvc,
			repo:    bbsRepo,
			wantErr: true,
		},
		{
			name:    "ssh clone URL no credentials",
			user:    user,
			extSvc:  bbsSSHExtsvc,
			repo:    bbsSSHRepo,
			wantErr: true,
		},
		{
			name:    "ssh clone URL no credentials admin",
			user:    admin,
			extSvc:  bbsSSHExtsvc,
			repo:    bbsSSHRepo,
			wantErr: true,
		},
		{
			name:   "ssh clone URL SSH credential",
			user:   admin,
			extSvc: bbsSSHExtsvc,
			repo:   bbsSSHRepo,
			credentials: &auth.OAuthBearerTokenWithSSH{
				OAuthBearerToken: auth.OAuthBearerToken{Token: "test"},
				PrivateKey:       "private key",
				PublicKey:        "public key",
				Passphrase:       "passphrase",
			},
			wantPushConfig: &gitprotocol.PushConfig{
				RemoteURL:  "ssh://git@bitbucket.sgdev.org:7999/" + string(bbsSSHRepo.Name),
				PrivateKey: "private key",
				Passphrase: "passphrase",
			},
		},
		{
			name:        "ssh clone URL non-SSH credential",
			user:        admin,
			extSvc:      bbsSSHExtsvc,
			repo:        bbsSSHRepo,
			credentials: &auth.OAuthBearerToken{Token: "test"},
			wantErr:     true,
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.credentials != nil {
				cred, err := bstore.UserCredentials().Create(ctx, database.UserCredentialScope{
					Domain:              database.UserCredentialDomainBatches,
					UserID:              tt.user.ID,
					ExternalServiceType: tt.repo.ExternalRepo.ServiceType,
					ExternalServiceID:   tt.repo.ExternalRepo.ServiceID,
				}, tt.credentials)
				if err != nil {
					t.Fatal(err)
				}
				defer func() { bstore.UserCredentials().Delete(ctx, cred.ID) }()
			}

			batchSpec := bt.CreateBatchSpec(t, ctx, bstore, fmt.Sprintf("reconciler-credentials-%d", i), tt.user.ID, 0)
			batchChange := bt.CreateBatchChange(t, ctx, bstore, fmt.Sprintf("reconciler-credentials-%d", i), tt.user.ID, batchSpec.ID)

			plan.Changeset = &btypes.Changeset{
				OwnedByBatchChangeID: batchChange.ID,
				RepoID:               tt.repo.ID,
			}
			plan.ChangesetSpec = bt.BuildChangesetSpec(t, bt.TestSpecOpts{
				HeadRef:    "refs/heads/my-branch",
				Published:  true,
				CommitDiff: "testdiff",
			})

			gitClient := &bt.FakeGitserverClient{ResponseErr: nil}
			fakeSource := &stesting.FakeChangesetSource{Svc: tt.extSvc}
			sourcer := stesting.NewFakeSourcer(nil, fakeSource)

			err := executePlan(
				actor.WithActor(ctx, actor.FromUser(tt.user.ID)),
				logtest.Scoped(t),
				gitClient,
				sourcer,
				true,
				bstore,
				plan,
			)

			if !tt.wantErr && err != nil {
				t.Fatalf("executing plan failed: %s", err)
			}
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error but got none")
				} else {
					return
				}
			}

			if diff := cmp.Diff(tt.wantPushConfig, gitClient.CreateCommitFromPatchReq.Push); diff != "" {
				t.Errorf("unexpected push options:\n%s", diff)
			}
		})
	}
}

func TestDecorateChangesetBody(t *testing.T) {
	ctx := context.Background()

	ns := database.NewMockNamespaceStore()
	ns.GetByIDFunc.SetDefaultHook(func(_ context.Context, _ int32, user int32) (*database.Namespace, error) {
		return &database.Namespace{Name: "my-user", User: user}, nil
	})

	internalClient = &mockInternalClient{externalURL: "https://sourcegraph.test"}
	defer func() { internalClient = internalapi.Client }()

	fs := &FakeStore{
		GetBatchChangeMock: func(ctx context.Context, opts store.GetBatchChangeOpts) (*btypes.BatchChange, error) {
			return &btypes.BatchChange{ID: 1234, Name: "reconciler-test-batch-change"}, nil
		},
	}

	cs := bt.BuildChangeset(bt.TestChangesetOpts{OwnedByBatchChange: 1234})

	wantLink := "[_Created by Sourcegraph batch change `my-user/reconciler-test-batch-change`._](https://sourcegraph.test/users/my-user/batch-changes/reconciler-test-batch-change)"

	for name, tc := range map[string]struct {
		body string
		want string
	}{
		"no template": {
			body: "body",
			want: "body\n\n" + wantLink,
		},
		"embedded template": {
			body: "body body ${{ batch_change_link }} body body",
			want: "body body " + wantLink + " body body",
		},
		"leading template": {
			body: "${{ batch_change_link }}\n\nbody body",
			want: wantLink + "\n\nbody body",
		},
		"weird spacing": {
			body: "${{     batch_change_link}}\n\nbody body",
			want: wantLink + "\n\nbody body",
		},
	} {
		t.Run(name, func(t *testing.T) {
			have, err := decorateChangesetBody(ctx, fs, ns, cs, tc.body)
			assert.NoError(t, err)
			assert.Equal(t, tc.want, have)
		})
	}
}

func TestHandleArchivedRepo(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		ch := &btypes.Changeset{ExternalState: btypes.ChangesetExternalStateDraft}
		repo := &types.Repo{Archived: false}

		store := repos.NewMockStore()
		store.UpdateRepoFunc.SetDefaultReturn(repo, nil)

		err := handleArchivedRepo(ctx, store, repo, ch)
		assert.NoError(t, err)
		assert.True(t, repo.Archived)
		assert.Equal(t, btypes.ChangesetExternalStateReadOnly, ch.ExternalState)
		assert.NotEmpty(t, store.UpdateRepoFunc.History())
	})

	t.Run("store error", func(t *testing.T) {
		ch := &btypes.Changeset{ExternalState: btypes.ChangesetExternalStateDraft}
		repo := &types.Repo{Archived: false}

		store := repos.NewMockStore()
		want := errors.New("")
		store.UpdateRepoFunc.SetDefaultReturn(nil, want)

		have := handleArchivedRepo(ctx, store, repo, ch)
		assert.Error(t, have)
		assert.ErrorIs(t, have, want)
		assert.True(t, repo.Archived)
		assert.Equal(t, btypes.ChangesetExternalStateDraft, ch.ExternalState)
		assert.NotEmpty(t, store.UpdateRepoFunc.History())
	})
}

func TestBatchChangeURL(t *testing.T) {
	ctx := context.Background()

	t.Run("errors", func(t *testing.T) {
		for name, tc := range map[string]*mockInternalClient{
			"ExternalURL error": {err: errors.New("foo")},
			"invalid URL":       {externalURL: "foo://:bar"},
		} {
			t.Run(name, func(t *testing.T) {
				internalClient = tc
				defer func() { internalClient = internalapi.Client }()

				if _, err := batchChangeURL(ctx, nil, nil); err == nil {
					t.Error("unexpected nil error")
				}
			})
		}
	})

	t.Run("success", func(t *testing.T) {
		internalClient = &mockInternalClient{externalURL: "https://sourcegraph.test"}
		defer func() { internalClient = internalapi.Client }()

		url, err := batchChangeURL(
			ctx,
			&database.Namespace{Name: "foo", Organization: 123},
			&btypes.BatchChange{Name: "bar"},
		)
		if err != nil {
			t.Errorf("unexpected non-nil error: %v", err)
		}
		if want := "https://sourcegraph.test/organizations/foo/batch-changes/bar"; url != want {
			t.Errorf("unexpected URL: have=%q want=%q", url, want)
		}
	})
}

func TestNamespaceURL(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct {
		ns   *database.Namespace
		want string
	}{
		"user": {
			ns:   &database.Namespace{User: 123, Name: "user"},
			want: "/users/user",
		},
		"org": {
			ns:   &database.Namespace{Organization: 123, Name: "org"},
			want: "/organizations/org",
		},
		"neither": {
			ns:   &database.Namespace{Name: "user"},
			want: "/users/user",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if have := namespaceURL(tc.ns); have != tc.want {
				t.Errorf("unexpected URL: have=%q want=%q", have, tc.want)
			}
		})
	}
}

type mockInternalClient struct {
	externalURL string
	err         error
}

func (c *mockInternalClient) ExternalURL(ctx context.Context) (string, error) {
	return c.externalURL, c.err
}

type mockRepoArchivedError struct{}

func (mockRepoArchivedError) Archived() bool     { return true }
func (mockRepoArchivedError) Error() string      { return "mock repo archived" }
func (mockRepoArchivedError) NonRetryable() bool { return true }
