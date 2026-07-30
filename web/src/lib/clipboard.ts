// copyText copies to the clipboard and reports whether it actually worked.
//
// navigator.clipboard is only defined in a SECURE CONTEXT (https, or
// http://localhost). HIMS is commonly reached over plain http on a LAN IP,
// where `navigator.clipboard?.writeText(...)` silently does nothing — the
// operator thinks they copied a password and pastes stale content. The
// execCommand fallback still works there, and the boolean lets callers show an
// honest "copy failed — select it manually" instead of a fake success tick.
export async function copyText(text: string): Promise<boolean> {
  try {
    if (navigator.clipboard && window.isSecureContext) {
      await navigator.clipboard.writeText(text)
      return true
    }
  } catch {
    // fall through to the legacy path
  }
  try {
    const ta = document.createElement('textarea')
    ta.value = text
    // Keep it off-screen but still focusable/selectable.
    ta.style.position = 'fixed'
    ta.style.top = '-1000px'
    ta.setAttribute('readonly', '')
    document.body.appendChild(ta)
    ta.select()
    const ok = document.execCommand('copy')
    ta.remove()
    return ok
  } catch {
    return false
  }
}
