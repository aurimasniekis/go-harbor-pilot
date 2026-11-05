import { createFileRoute, Outlet } from '@tanstack/react-router'

export const Route = createFileRoute('/traces')({
  component: () => <Outlet />,
})
