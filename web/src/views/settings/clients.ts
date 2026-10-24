import type { KeyScope } from '../../lib/types'

/** The name the connect link runs the shared iPhone shortcut by; its iCloud link comes from the server (RITM_SHORTCUT_URL). */
export const SHORTCUT_NAME = 'Send to Ritm'

/**
 * A shortcuts:// link that runs the shortcut with the upload URL and key as text input; the shortcut saves them as its
 * config (README, "Upload from an iPhone Shortcut"). `ritm` versions the payload.
 */
export function shortcutConnectURL(uploadURL: string, key: string): string {
  const config = JSON.stringify({ ritm: 1, url: uploadURL, key })
  return `shortcuts://run-shortcut?name=${encodeURIComponent(SHORTCUT_NAME)}&input=text&text=${encodeURIComponent(config)}`
}

/** The kinds of clients an API key is for, one per key scope. */
export const CLIENTS: Record<KeyScope, { label: string; icon: string; blurb: string; keyNote: string; defaultName: string }> = {
  ingest: {
    label: 'iPhone Shortcut',
    icon: 'phone',
    blurb: 'Send export.zip to Ritm straight from the Health share sheet. Set up by scanning a code, no typing.',
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
  read: {
    label: 'AI assistant or script',
    icon: 'bot',
    blurb: 'Let Claude or another MCP client, or your own scripts and integrations, read and analyse your data.',
    keyNote: 'A read key reads health data over the REST API and the MCP server at /mcp; it cannot change anything.',
    defaultName: 'Claude',
  },
}
