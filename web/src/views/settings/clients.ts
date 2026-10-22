import type { KeyScope } from '../../lib/types'

/** The kinds of clients an API key is for, one per key scope. */
export const CLIENTS: Record<KeyScope, { label: string; icon: string; blurb: string; keyNote: string; defaultName: string }> = {
  ingest: {
    label: 'iPhone Shortcut',
    icon: 'phone',
    blurb: 'Send export.zip to Ritm straight from the Health share sheet.',
    keyNote: 'An ingest key can only upload exports and read their import status.',
    defaultName: 'iPhone Shortcut',
  },
  monitor: {
    label: 'Uptime Kuma',
    icon: 'activity',
    blurb: 'Get an alert when no new data arrives. Ritm itself sends no notifications.',
    keyNote: 'A monitor key can only read data freshness.',
    defaultName: 'Uptime Kuma',
  },
  mcp: {
    label: 'AI assistant',
    icon: 'bot',
    blurb: 'Let Claude or another MCP client read and analyse your data.',
    keyNote: 'An MCP key works only with the MCP endpoint at /mcp.',
    defaultName: 'Claude',
  },
}
