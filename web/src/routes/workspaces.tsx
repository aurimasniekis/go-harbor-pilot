import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Dialog, MutationError, SelectField, TextField, formStyles as f } from '@/components/form'
import { KeyValueEditor } from '@/components/KeyValueEditor'
import { TerraformEditWarning } from '@/components/ResourceBadges'
import { NameCell, ResourceList } from '@/features/config/ResourceList'
import { Group, Pill, Stack } from '@mantine/core'
import { SettingsHeader } from '@/features/settings/SettingsHeader'
import { resourceListQuery } from '@/lib/api/queries'
import { byId, useSaveResource } from '@/lib/api/resources'
import type { ProjectRes, ProjectSpec, WorkspaceRes, WorkspaceSpec } from '@/lib/api/types'

export const Route = createFileRoute('/workspaces')({
  component: Workspaces,
})

function WorkspaceDialog({ ws, onClose }: { ws: WorkspaceRes | null; onClose: () => void }) {
  const [name, setName] = useState(ws?.name ?? '')
  const [displayName, setDisplayName] = useState(ws?.spec.display_name ?? '')
  const [description, setDescription] = useState(ws?.spec.description ?? '')
  const save = useSaveResource<WorkspaceSpec>('workspaces')
  return (
    <Dialog open title={ws ? `Edit workspace ${ws.name}` : 'Add workspace'} onClose={onClose}>
      <TerraformEditWarning r={ws} />
      <TextField label="Name" value={name} onChange={setName} disabled={!!ws} required />
      <TextField label="Display name" value={displayName} onChange={setDisplayName} maxLength={120} />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={1000} />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name}
          onClick={() => save.mutate({ existing: ws ?? undefined, body: { name, spec: { display_name: displayName, description } } }, { onSuccess: onClose })}>
          Save workspace
        </Button>
      </div>
    </Dialog>
  )
}

function ProjectDialog({ project, onClose }: { project: ProjectRes | null; onClose: () => void }) {
  const workspaces = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const [name, setName] = useState(project?.name ?? '')
  const [s, setS] = useState<ProjectSpec>(project?.spec ?? { workspace_id: '', display_name: '', description: '', external_refs: {} })
  const [refsValid, setRefsValid] = useState(true)
  const save = useSaveResource<ProjectSpec>('projects')
  return (
    <Dialog open title={project ? `Edit project ${project.name}` : 'Add project'} onClose={onClose}>
      <TerraformEditWarning r={project} />
      <TextField label="Name" value={name} onChange={setName} disabled={!!project} required />
      <SelectField label="Workspace" value={s.workspace_id} onChange={(v) => setS({ ...s, workspace_id: v })}
        options={[{ value: '', label: 'Select a workspace' }, ...(workspaces.data?.items ?? []).map((w) => ({ value: w.id, label: w.name }))]} />
      <TextField label="Display name" value={s.display_name} onChange={(v) => setS({ ...s, display_name: v })} maxLength={120} />
      <TextField label="Description" value={s.description} onChange={(v) => setS({ ...s, description: v })} maxLength={1000} />
      <KeyValueEditor label="External references" value={s.external_refs} onChange={(v, ok) => { setS({ ...s, external_refs: v }); setRefsValid(ok) }}
        hint="Trusted references such as spaceman.space=spc_… or git.repo=github.com/org/repo." />
      <MutationError error={save.error} />
      <div className={f.actions}>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} disabled={!name || !s.workspace_id || !refsValid}
          onClick={() => save.mutate({ existing: project ?? undefined, body: { name, spec: s } }, { onSuccess: onClose })}>
          Save project
        </Button>
      </div>
    </Dialog>
  )
}

function Workspaces() {
  const ws = byId(useQuery(resourceListQuery<WorkspaceRes>('workspaces')).data?.items)
  return (
    <Stack gap="lg">
      <SettingsHeader />
      <ResourceList<WorkspaceRes>
        plural="workspaces"
        title="Workspaces"
        subtitle="Workspaces group projects; projects are what usage and budgets are attributed to."
        noun="workspace"
        empty="Create a workspace to group projects."
        nameCell={(r) => <NameCell name={r.name} display={r.spec.display_name} />}
        columns={[{ header: 'Description', mobile: 'meta', cell: (r) => r.spec.description || <span className="muted">—</span> }]}
        dialog={(r, close) => <WorkspaceDialog ws={r} onClose={close} />}
      />
      <ResourceList<ProjectRes>
        plural="projects"
        title="Projects"
        noun="project"
        empty="Create a project inside a workspace."
        nameCell={(r) => <NameCell name={r.name} display={r.spec.display_name} />}
        columns={[
          { header: 'Workspace', mobile: 'meta', cell: (r) => ws[r.spec.workspace_id]?.spec.display_name || ws[r.spec.workspace_id]?.name || r.spec.workspace_id },
          {
            header: 'References',
            mobile: 'line',
            cell: (r) => (
              <Group gap={4} wrap="wrap">
                {Object.entries(r.spec.external_refs).map(([k, v]) => (
                  <Pill key={k} size="sm">
                    {k}: {v}
                  </Pill>
                ))}
              </Group>
            ),
          },
        ]}
        dialog={(r, close) => <ProjectDialog project={r} onClose={close} />}
      />
    </Stack>
  )
}
