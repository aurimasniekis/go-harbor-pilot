import { useState } from 'react'
import { createFileRoute } from '@tanstack/react-router'
import { useQuery } from '@tanstack/react-query'
import { Button } from '@/components/ui'
import { Dialog, DialogActions, MutationError, NameField, SelectField, TextField, slugify, useFieldErrors } from '@/components/form'
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
  const [submitted, setSubmitted] = useState(false)
  const save = useSaveResource<WorkspaceSpec>('workspaces')
  const errs = useFieldErrors(save.error)
  const dirty = name !== (ws?.name ?? '') || displayName !== (ws?.spec.display_name ?? '') || description !== (ws?.spec.description ?? '')
  const submit = () => {
    setSubmitted(true)
    if (!name) return
    save.mutate({ existing: ws ?? undefined, body: { name, spec: { display_name: displayName, description } } }, { onSuccess: onClose })
  }
  return (
    <Dialog open title={ws ? `Edit workspace ${ws.spec.display_name || ws.name}` : 'Add workspace'} onClose={onClose} dirty={dirty}>
      <TerraformEditWarning r={ws} />
      <TextField label="Display name" value={displayName} onChange={(v) => { setDisplayName(v); if (!ws && (!name || name === slugify(displayName))) setName(slugify(v)) }}
        maxLength={120} placeholder="Acme" autoFocus={!ws} error={errs['display_name']} />
      <NameField value={name} onChange={setName} locked={!!ws} error={(submitted && !name ? 'Enter a short name.' : undefined) ?? errs['name']} />
      <TextField label="Description" value={description} onChange={setDescription} maxLength={1000} error={errs['description']} />
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={submit}>
          {ws ? 'Save workspace' : 'Add workspace'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function ProjectDialog({ project, onClose }: { project: ProjectRes | null; onClose: () => void }) {
  const workspaces = useQuery(resourceListQuery<WorkspaceRes>('workspaces'))
  const wsItems = workspaces.data?.items ?? []
  const [name, setName] = useState(project?.name ?? '')
  const [s, setS] = useState<ProjectSpec>(project?.spec ?? { workspace_id: '', display_name: '', description: '', external_refs: {} })
  const [refsValid, setRefsValid] = useState(true)
  const [submitted, setSubmitted] = useState(false)
  const save = useSaveResource<ProjectSpec>('projects')
  const errs = useFieldErrors(save.error)
  // With a single workspace there is nothing to choose.
  const workspaceID = s.workspace_id || (wsItems.length === 1 ? wsItems[0]!.id : '')
  const dirty = name !== (project?.name ?? '') || JSON.stringify(s) !== JSON.stringify(project?.spec ?? { workspace_id: '', display_name: '', description: '', external_refs: {} })
  const submit = () => {
    setSubmitted(true)
    if (!name || !workspaceID || !refsValid) return
    save.mutate({ existing: project ?? undefined, body: { name, spec: { ...s, workspace_id: workspaceID } } }, { onSuccess: onClose })
  }
  return (
    <Dialog open title={project ? `Edit project ${project.spec.display_name || project.name}` : 'Add project'} onClose={onClose} dirty={dirty}>
      <TerraformEditWarning r={project} />
      <SelectField label="Workspace" value={workspaceID} onChange={(v) => setS({ ...s, workspace_id: v })} placeholder="Choose a workspace" required
        options={wsItems.map((w) => ({ value: w.id, label: w.spec.display_name || w.name }))}
        error={(submitted && !workspaceID ? 'Choose a workspace.' : undefined) ?? errs['workspace_id']} />
      <TextField label="Display name" value={s.display_name} maxLength={120} placeholder="Web app" autoFocus={!project} error={errs['display_name']}
        onChange={(v) => { setS({ ...s, display_name: v }); if (!project && (!name || name === slugify(s.display_name))) setName(slugify(v)) }} />
      <NameField value={name} onChange={setName} locked={!!project} error={(submitted && !name ? 'Enter a short name.' : undefined) ?? errs['name']} />
      <TextField label="Description" value={s.description} onChange={(v) => setS({ ...s, description: v })} maxLength={1000} error={errs['description']} />
      <KeyValueEditor label="External references" value={s.external_refs} onChange={(v, ok) => { setS({ ...s, external_refs: v }); setRefsValid(ok) }}
        hint="Optional. One key=value per line, such as git.repo=github.com/org/repo or spaceman.space=spc_…" error={errs['external_refs']} />
      <MutationError error={save.error} />
      <DialogActions>
        <Button onClick={onClose}>Cancel</Button>
        <Button variant="primary" busy={save.isPending} onClick={submit}>
          {project ? 'Save project' : 'Add project'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}

function Workspaces() {
  const ws = byId(useQuery(resourceListQuery<WorkspaceRes>('workspaces')).data?.items)
  const projects = useQuery(resourceListQuery<ProjectRes>('projects'))
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
        columns={[
          { header: 'Description', mobile: 'meta', cell: (r) => r.spec.description || <span className="muted">—</span> },
        ]}
        usedBy={(r) => (projects.data?.items ?? []).filter((p) => p.spec.workspace_id === r.id).map((p) => `project ${p.spec.display_name || p.name}`)}
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
