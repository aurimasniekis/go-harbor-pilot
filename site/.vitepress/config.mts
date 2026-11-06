import { defineConfig } from 'vitepress';
import { withMermaid } from 'vitepress-plugin-mermaid';

const repo = 'https://github.com/aurimasniekis/go-harbor-pilot';

export default withMermaid(
  defineConfig({
    title: 'Harbor Pilot',
    description:
      'A self-hosted LLM gateway and policy control plane for coding agents: routing plans, subscriptions and API keys, quotas and budgets, secret masking and explainable decisions.',
    lang: 'en-US',
    // GitHub Pages project site: https://aurimasniekis.github.io/go-harbor-pilot/
    base: '/go-harbor-pilot/',
    cleanUrls: true,
    lastUpdated: false,
    srcExclude: ['README.md'],
    head: [
      ['link', { rel: 'icon', type: 'image/svg+xml', href: '/go-harbor-pilot/logo.svg' }],
      ['link', { rel: 'icon', type: 'image/png', sizes: '32x32', href: '/go-harbor-pilot/logo/logo-32.png' }],
      ['link', { rel: 'apple-touch-icon', href: '/go-harbor-pilot/logo/logo-256.png' }],
      ['meta', { name: 'theme-color', content: '#4876d6' }],
    ],
    markdown: {
      theme: { light: 'github-light', dark: 'github-dark' },
    },
    themeConfig: {
      logo: '/logo.svg',
      nav: [
        { text: 'Get started', link: '/guide/what-is-harbor-pilot' },
        { text: 'Guides', link: '/operators/' },
        { text: 'Tools', link: '/tools/' },
        { text: 'Security', link: '/security/' },
        { text: 'Reference', link: '/reference/configuration' },
      ],
      sidebar: [
        {
          text: 'Get started',
          items: [
            { text: 'What is Harbor Pilot?', link: '/guide/what-is-harbor-pilot' },
            { text: 'Concepts', link: '/guide/concepts' },
            { text: 'Try the demo', link: '/guide/demo' },
            { text: 'Install with Docker', link: '/guide/install-docker' },
            { text: 'Install the binary (systemd)', link: '/guide/install-binary' },
            { text: 'Behind a reverse proxy', link: '/guide/reverse-proxy' },
            { text: 'First run', link: '/guide/first-run' },
            { text: 'Connect your first tool', link: '/guide/first-tool' },
          ],
        },
        {
          text: 'Everyday use',
          items: [
            { text: 'Overview', link: '/operators/' },
            { text: 'Home and what needs attention', link: '/operators/home' },
            { text: 'Requests and "why this route"', link: '/operators/requests' },
            { text: 'Spend', link: '/operators/spend' },
            { text: 'Limits: quotas and budgets', link: '/operators/limits' },
          ],
        },
        {
          text: 'Configure',
          items: [
            { text: 'Providers and accounts', link: '/operators/providers' },
            { text: 'Subscriptions and CLI bridges', link: '/operators/subscriptions' },
            { text: 'Clients and access tokens', link: '/operators/clients' },
            { text: 'Routing plans', link: '/operators/routing' },
            { text: 'Privacy and masking', link: '/operators/privacy' },
            { text: 'Captured content', link: '/operators/captured-content' },
            { text: 'Alerts', link: '/operators/alerts' },
            { text: 'Events (Pulse and webhooks)', link: '/operators/events' },
            { text: 'Terraform', link: '/operators/terraform' },
          ],
        },
        {
          text: 'Administer',
          items: [
            { text: 'People, API tokens and sign-in', link: '/operators/access' },
            { text: 'Audit log', link: '/operators/audit' },
            { text: 'Diagnostics', link: '/operators/diagnostics' },
            { text: 'Backup and restore', link: '/operators/backup' },
            { text: 'Upgrades and rollback', link: '/operators/upgrades' },
            { text: 'Monitoring and capacity', link: '/operators/monitoring' },
            { text: 'Troubleshooting', link: '/operators/troubleshooting' },
            { text: 'Migrating from Quota Harbor', link: '/operators/migrate-quota-harbor' },
          ],
        },
        {
          text: 'Tools',
          items: [
            { text: 'Overview', link: '/tools/' },
            { text: 'Claude Code', link: '/tools/claude-code' },
            { text: 'Codex', link: '/tools/codex' },
            { text: 'OpenCode', link: '/tools/opencode' },
            { text: 'Pi', link: '/tools/pi' },
            { text: 'SDKs and API clients', link: '/tools/sdks' },
            { text: 'Agent orchestrators (SpaceMan)', link: '/tools/orchestrators' },
          ],
        },
        {
          text: 'Security',
          items: [
            { text: 'Security model', link: '/security/' },
            { text: 'Secrets and the master key', link: '/security/secrets' },
            { text: 'Masking: guarantees and limits', link: '/security/masking' },
            { text: 'Known limitations', link: '/security/limitations' },
          ],
        },
        {
          text: 'Reference',
          items: [
            { text: 'Configuration', link: '/reference/configuration' },
            { text: 'Command line', link: '/reference/cli' },
            { text: 'Gateway endpoints', link: '/reference/gateway' },
            { text: 'Control-plane API', link: '/reference/api' },
            { text: 'Event catalog', link: '/reference/events' },
            { text: 'Compatibility matrix', link: '/reference/compatibility' },
          ],
        },
      ],
      socialLinks: [{ icon: 'github', link: repo }],
      editLink: {
        pattern: `${repo}/edit/main/site/:path`,
        text: 'Edit this page on GitHub',
      },
      search: { provider: 'local' },
      footer: {
        message: 'Released under the Apache-2.0 License.',
        copyright: 'Copyright © 2026 Aurimas Niekis',
      },
    },
    mermaid: {},
  }),
);
