// Copy text to the clipboard, working outside secure contexts too.
//
// Homelab instances are commonly browsed over plain http (http://server:7377),
// where navigator.clipboard is undefined — `navigator.clipboard?.writeText`
// silently does nothing there. Fall back to a hidden textarea + execCommand,
// which still works in every browser over http.
export async function copyText(text: string): Promise<boolean> {
  if (navigator.clipboard) {
    try {
      await navigator.clipboard.writeText(text);
      return true;
    } catch {
      // fall through to the legacy path (permission denied, embedded view, …)
    }
  }
  const holder = document.createElement('textarea');
  holder.value = text;
  holder.setAttribute('readonly', '');
  holder.style.position = 'fixed';
  holder.style.opacity = '0';
  document.body.appendChild(holder);
  holder.select();
  let done = false;
  try {
    done = document.execCommand('copy');
  } catch {
    done = false;
  }
  holder.remove();
  return done;
}
