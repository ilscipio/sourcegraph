import { Args, useMemo } from '@storybook/addons'
import { DecoratorFn, Story, Meta } from '@storybook/react'
import { addDays, subDays } from 'date-fns'
import { Observable, of } from 'rxjs'
import { MATCH_ANY_PARAMETERS, WildcardMockLink } from 'wildcard-mock-link'

import { getDocumentNode } from '@sourcegraph/http-client'
import { MockedTestProvider } from '@sourcegraph/shared/src/testing/apollo'

import { WebStory } from '../../../components/WebStory'
import {
    ApplyPreviewStatsFields,
    BatchSpecApplyPreviewConnectionFields,
    BatchSpecFields,
    ChangesetApplyPreviewFields,
    ExternalServiceKind,
} from '../../../graphql-operations'
import { GET_LICENSE_AND_USAGE_INFO } from '../list/backend'
import { getLicenseAndUsageInfoResult } from '../list/testData'

import { BATCH_SPEC_BY_ID } from './backend'
import { BatchChangePreviewPage, NewBatchChangePreviewPage } from './BatchChangePreviewPage'
import { hiddenChangesetApplyPreviewStories, visibleChangesetApplyPreviewNodeStories } from './list/storyData'

const decorator: DecoratorFn = story => <div className="p-3 container">{story()}</div>

const config: Meta = {
    title: 'web/batches/preview/BatchChangePreviewPage',
    decorators: [decorator],

    parameters: {
        chromatic: {
            viewports: [320, 576, 978, 1440],
            disableSnapshot: false,
        },
    },
    argTypes: {
        supersedingBatchSpec: {
            control: { type: 'boolean' },
            defaultValue: false,
        },
        viewerCanAdminister: {
            control: { type: 'boolean' },
            defaultValue: true,
        },
    },
}

export default config

const nodes: ChangesetApplyPreviewFields[] = [
    ...Object.values(visibleChangesetApplyPreviewNodeStories(false)),
    ...Object.values(hiddenChangesetApplyPreviewStories),
]

const batchSpec = (props: Args): BatchSpecFields => ({
    appliesToBatchChange: null,
    createdAt: subDays(new Date(), 5).toISOString(),
    creator: {
        __typename: 'User',
        url: '/users/alice',
        username: 'alice',
    },
    description: {
        __typename: 'BatchChangeDescription',
        name: 'awesome-batch-change',
        description: 'This is the description',
    },
    diffStat: {
        __typename: 'DiffStat',
        added: 10,
        changed: 8,
        deleted: 10,
    },
    expiresAt: addDays(new Date(), 7).toISOString(),
    id: 'specid',
    namespace: {
        __typename: 'User',
        namespaceName: 'alice',
        url: '/users/alice',
    },
    supersedingBatchSpec: props.supersedingBatchSpec
        ? {
              __typename: 'BatchSpec',
              createdAt: subDays(new Date(), 1).toISOString(),
              applyURL: '/users/alice/batch-changes/apply/newspecid',
          }
        : null,
    viewerCanAdminister: props.viewerCanAdminister,
    viewerBatchChangesCodeHosts: {
        __typename: 'BatchChangesCodeHostConnection',
        totalCount: 0,
        nodes: [],
    },
    originalInput: 'name: awesome-batch-change\ndescription: somestring',
    applyPreview: {
        __typename: 'ChangesetApplyPreviewConnection',
        stats: {
            archive: 18,
        },
        totalCount: 18,
    },
})

// This has to be a link so we can return as many mock responses are required
// for the time the storybook is open.
const batchSpecByIDLink = (spec: BatchSpecFields): WildcardMockLink =>
    new WildcardMockLink([
        {
            request: {
                query: getDocumentNode(BATCH_SPEC_BY_ID),
                variables: {
                    batchSpec: '123123',
                },
            },
            result: {
                data: {
                    node: {
                        __typename: 'BatchSpec',
                        ...spec,
                    },
                },
            },
            nMatches: Number.POSITIVE_INFINITY,
        },
    ])

const fetchBatchSpecCreate = (props: Args) => batchSpecByIDLink(batchSpec(props))

const fetchBatchSpecMissingCredentials = (props: Args) =>
    batchSpecByIDLink({
        ...batchSpec(props),
        viewerBatchChangesCodeHosts: {
            __typename: 'BatchChangesCodeHostConnection',
            totalCount: 2,
            nodes: [
                {
                    externalServiceKind: ExternalServiceKind.GITHUB,
                    externalServiceURL: 'https://github.com/',
                },
                {
                    externalServiceKind: ExternalServiceKind.GITLAB,
                    externalServiceURL: 'https://gitlab.com/',
                },
            ],
        },
    })

const fetchBatchSpecUpdate = (props: Args) =>
    batchSpecByIDLink({
        ...batchSpec(props),
        appliesToBatchChange: {
            id: 'somebatch',
            name: 'awesome-batch-change',
            url: '/users/alice/batch-changes/awesome-batch-change',
        },
    })

const fetchExceedsLicense = (props: Args) =>
    new WildcardMockLink([
        {
            request: {
                query: getDocumentNode(BATCH_SPEC_BY_ID),
                variables: {
                    batchSpec: '123123',
                },
            },
            result: {
                data: {
                    node: {
                        __typename: 'BatchSpec',
                        ...batchSpec(props),
                    },
                },
            },
            nMatches: Number.POSITIVE_INFINITY,
        },
        {
            request: {
                query: getDocumentNode(GET_LICENSE_AND_USAGE_INFO),
                variables: MATCH_ANY_PARAMETERS,
            },
            result: { data: getLicenseAndUsageInfoResult(false, true) },
            nMatches: Number.POSITIVE_INFINITY,
        },
    ])

const queryApplyPreviewStats = (): Observable<ApplyPreviewStatsFields['stats']> =>
    of({
        close: 10,
        detach: 10,
        import: 10,
        publish: 10,
        publishDraft: 10,
        push: 10,
        reopen: 10,
        undraft: 10,
        update: 10,
        reattach: 10,
        archive: 18,
        added: 5,
        modified: 10,
        removed: 3,
    })

const queryChangesetApplyPreview = (): Observable<BatchSpecApplyPreviewConnectionFields> =>
    of({
        pageInfo: {
            endCursor: null,
            hasNextPage: false,
        },
        totalCount: nodes.length,
        nodes,
    })

const queryEmptyChangesetApplyPreview = (): Observable<BatchSpecApplyPreviewConnectionFields> =>
    of({
        pageInfo: {
            endCursor: null,
            hasNextPage: false,
        },
        totalCount: 0,
        nodes: [],
    })

const queryEmptyFileDiffs = () => of({ totalCount: 0, pageInfo: { endCursor: null, hasNextPage: false }, nodes: [] })

export const Create: Story = args => {
    const link = useMemo(() => fetchBatchSpecCreate(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <BatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

export const Update: Story = args => {
    const link = useMemo(() => fetchBatchSpecUpdate(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <BatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

export const MissingCredentials: Story = args => {
    const link = useMemo(() => fetchBatchSpecMissingCredentials(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <BatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

MissingCredentials.storyName = 'Missing credentials'

export const SpecFile: Story = args => {
    const link = useMemo(() => fetchBatchSpecCreate(args), [args])
    return (
        <WebStory initialEntries={['/users/alice/batch-changes/awesome-batch-change?tab=spec']}>
            {props => (
                <MockedTestProvider link={link}>
                    <BatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

SpecFile.storyName = 'Spec file'

export const NoChangesets: Story = args => {
    const link = useMemo(() => fetchBatchSpecCreate(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <BatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryEmptyChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

NoChangesets.storyName = 'No changesets'

export const CreateNewStory: Story = args => {
    const link = useMemo(() => fetchBatchSpecCreate(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <NewBatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

CreateNewStory.storyName = 'Create (New)'

export const ExceedsLicenseStory: Story = args => {
    const link = useMemo(() => fetchExceedsLicense(args), [args])
    return (
        <WebStory>
            {props => (
                <MockedTestProvider link={link}>
                    <NewBatchChangePreviewPage
                        {...props}
                        expandChangesetDescriptions={true}
                        batchSpecID="123123"
                        queryChangesetApplyPreview={queryChangesetApplyPreview}
                        queryChangesetSpecFileDiffs={queryEmptyFileDiffs}
                        queryApplyPreviewStats={queryApplyPreviewStats}
                        authenticatedUser={{
                            url: '/users/alice',
                            displayName: 'Alice',
                            username: 'alice',
                            email: 'alice@email.test',
                        }}
                    />
                </MockedTestProvider>
            )}
        </WebStory>
    )
}

ExceedsLicenseStory.storyName = 'Exceeds License (New)'
