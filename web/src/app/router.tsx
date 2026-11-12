import { createBrowserRouter, Navigate } from 'react-router-dom';
import { SessionGuard } from './guard';
import { Shell } from './shell';
import { LoginPage } from '@/features/auth/login-page';
import { PasswordPage } from '@/features/auth/password-page';
import { WorkspacesPage } from '@/features/workspaces/workspaces-page';
import { OverviewPage } from '@/features/resources/overview-page';
import { NotebooksPage } from '@/features/notebooks/notebooks-page';
import { NotebookDetailPage } from '@/features/notebooks/notebook-detail-page';
import { CreateNotebookDialog } from '@/features/notebooks/create-notebook';
import { EditNotebookPage } from '@/features/notebooks/edit-notebook-page';
import { LegacyWorkspaceRedirect, RouteError } from './route-boundaries';
import { CreateGroupPage } from '@/features/groups/create-group-page';
import { GroupSettingsPage } from '@/features/groups/settings-page';
import { MembersPage } from '@/features/groups/members-page';
import { MemberEditorPage } from '@/features/groups/member-editor-page';
import { TransferOwnerPage } from '@/features/groups/transfer-owner-page';
import { QuotaPage } from '@/features/groups/quota-page';
import { EditQuotaPage } from '@/features/groups/edit-quota-page';
import { AdminIndex, AdminLayout } from '@/features/admin/admin-layout';
import { UsersPage } from '@/features/admin/users-page';
import { CreateUserPage } from '@/features/admin/create-user-page';
import { UserDetailPage } from '@/features/admin/user-detail-page';
import { GlobalRolesPage } from '@/features/admin/global-roles-page';
import { RolesPage } from '@/features/admin/roles-page';
import { RoleEditorPage } from '@/features/admin/role-editor-page';
import { AuditsPage } from '@/features/admin/audits-page';
import { SessionsPage } from '@/features/security/sessions-page';
import { StoragePage } from '@/features/storage/storage-page';
import { WorkspaceDetailPage } from '@/features/storage/workspace-detail-page';
import { TransferWorkspaceOwnerPage } from '@/features/storage/transfer-owner-page';
import { TransferNotebookOwnerPage } from '@/features/notebooks/transfer-owner-page';
import { AccountLayout } from '@/features/account/account-layout';
import { ProfilePage } from '@/features/account/profile-page';
import { AccountPasswordPage } from '@/features/account/password-page';
import { PipelinesPage } from '@/features/pipelines/pipelines-page';
import { PipelineDetailPage, PipelineVersionPage } from '@/features/pipelines/pipeline-detail-page';
import {
  CreatePipelinePage,
  ImportPipelineVersionPage,
  SubmitPipelineRunPage,
} from '@/features/pipelines/pipeline-form-pages';
import { PipelineRunDetailPage } from '@/features/pipelines/run-detail-page';
import { ModelsPage } from '@/features/models/models-page';
import { ModelDetailPage } from '@/features/models/model-detail-page';
import { ModelVersionPage } from '@/features/models/version-detail-page';
import {
  CreateModelPage,
  EditModelPage,
  CreateModelVersionPage,
  EditModelVersionPage,
  CreateModelArtifactPage,
} from '@/features/models/model-form-pages';
import { RegisterOutputPage } from '@/features/models/register-output-page';
import { PublishModelPage, EditServingPage } from '@/features/models/serving-form-page';
import { ServingPage } from '@/features/models/serving-page';
import { ServingDetailPage } from '@/features/models/serving-detail-page';

export const router = createBrowserRouter([
  { path: '/login', element: <LoginPage />, errorElement: <RouteError /> },
  {
    element: <SessionGuard />,
    errorElement: <RouteError />,
    children: [
      { path: '/', element: <WorkspacesPage /> },
      { path: '/password', element: <PasswordPage /> },
      { path: '/workspaces/new', element: <CreateGroupPage /> },
      { path: '/spaces/new', element: <LegacyWorkspaceRedirect /> },
      { path: '/g/:groupId/*', element: <LegacyWorkspaceRedirect /> },
      {
        path: '/account',
        element: <AccountLayout />,
        children: [
          { index: true, element: <ProfilePage /> },
          { path: 'password', element: <AccountPasswordPage /> },
          { path: 'sessions', element: <SessionsPage /> },
        ],
      },
      {
        path: '/admin',
        element: <AdminLayout />,
        children: [
          { index: true, element: <AdminIndex /> },
          { path: 'users', element: <UsersPage /> },
          { path: 'users/new', element: <CreateUserPage /> },
          { path: 'users/:userId', element: <UserDetailPage /> },
          { path: 'users/:userId/roles', element: <GlobalRolesPage /> },
          { path: 'roles', element: <RolesPage /> },
          { path: 'roles/new', element: <RoleEditorPage /> },
          { path: 'roles/:roleId/edit', element: <RoleEditorPage /> },
          { path: 'audits', element: <AuditsPage /> },
        ],
      },
      {
        path: '/workspaces/:groupId',
        element: <Shell />,
        children: [
          { index: true, element: <OverviewPage /> },
          { path: 'settings', element: <GroupSettingsPage /> },
          { path: 'members', element: <MembersPage /> },
          { path: 'members/new', element: <MemberEditorPage /> },
          { path: 'members/transfer', element: <TransferOwnerPage /> },
          { path: 'members/:userId/edit', element: <MemberEditorPage /> },
          { path: 'quota', element: <QuotaPage /> },
          { path: 'quota/edit', element: <EditQuotaPage /> },
          {
            path: 'notebooks',
            element: <NotebooksPage />,
            children: [{ path: 'new', element: <CreateNotebookDialog /> }],
          },
          { path: 'notebooks/:notebookId', element: <NotebookDetailPage /> },
          { path: 'notebooks/:notebookId/edit', element: <EditNotebookPage /> },
          { path: 'notebooks/:notebookId/transfer', element: <TransferNotebookOwnerPage /> },
          { path: 'storage', element: <StoragePage /> },
          { path: 'storage/:workspaceId', element: <WorkspaceDetailPage /> },
          { path: 'storage/:workspaceId/transfer', element: <TransferWorkspaceOwnerPage /> },
          { path: 'pipelines', element: <PipelinesPage /> },
          { path: 'pipelines/new', element: <CreatePipelinePage /> },
          { path: 'pipelines/:pipelineId', element: <PipelineDetailPage /> },
          { path: 'pipelines/:pipelineId/versions/new', element: <ImportPipelineVersionPage /> },
          { path: 'pipelines/:pipelineId/versions/:versionId', element: <PipelineVersionPage /> },
          { path: 'pipelines/:pipelineId/run', element: <SubmitPipelineRunPage /> },
          { path: 'pipeline-runs/:runId', element: <PipelineRunDetailPage /> },
          { path: 'models', element: <ModelsPage /> },
          { path: 'models/new', element: <CreateModelPage /> },
          { path: 'models/register-output', element: <RegisterOutputPage /> },
          { path: 'models/:modelId', element: <ModelDetailPage /> },
          { path: 'models/:modelId/edit', element: <EditModelPage /> },
          { path: 'models/:modelId/versions/new', element: <CreateModelVersionPage /> },
          { path: 'models/:modelId/versions/:versionId', element: <ModelVersionPage /> },
          { path: 'models/:modelId/versions/:versionId/edit', element: <EditModelVersionPage /> },
          {
            path: 'models/:modelId/versions/:versionId/artifacts/new',
            element: <CreateModelArtifactPage />,
          },
          { path: 'kserve-services', element: <ServingPage /> },
          { path: 'kserve-services/new', element: <PublishModelPage /> },
          { path: 'kserve-services/:serviceId', element: <ServingDetailPage /> },
          { path: 'kserve-services/:serviceId/edit', element: <EditServingPage /> },
        ],
      },
      { path: '*', element: <Navigate to="/" replace /> },
    ],
  },
]);
