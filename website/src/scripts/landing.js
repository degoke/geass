// Copy the install command. Falls back to selecting the text when the Clipboard API is unavailable
// (it needs a secure context, so it is missing on plain-http previews).
const button = document.getElementById("copy");
const status = document.getElementById("copy-status");
const command = document.getElementById("install-cmd");

function commandText() {
  return command.textContent.replace(/^\s*\$\s*/, "").trim();
}

function announce(message) {
  status.textContent = message;
  window.setTimeout(() => {
    status.textContent = "";
  }, 2400);
}

button?.addEventListener("click", async () => {
  try {
    await navigator.clipboard.writeText(commandText());
    announce("Copied. Paste it on your server.");
  } catch {
    const range = document.createRange();
    range.selectNodeContents(command);
    const selection = window.getSelection();
    selection.removeAllRanges();
    selection.addRange(range);
    announce("Selected. Press Ctrl+C to copy.");
  }
});

// Tabs for the agent demo. Without JavaScript every panel stays visible, one under the other.
const tabs = [...document.querySelectorAll('.tabs [role="tab"]')];
const panels = tabs.map((tab) => document.getElementById(tab.getAttribute("aria-controls")));

function selectTab(next, focus = false) {
  tabs.forEach((tab, i) => {
    const on = tab === next;
    tab.setAttribute("aria-selected", String(on));
    tab.tabIndex = on ? 0 : -1;
    panels[i].hidden = !on;
  });
  if (focus) next.focus();
}

if (tabs.length) {
  selectTab(tabs[0]);
  tabs.forEach((tab, i) => {
    tab.addEventListener("click", () => selectTab(tab));
    tab.addEventListener("keydown", (event) => {
      const step = { ArrowRight: 1, ArrowLeft: -1 }[event.key];
      if (step) {
        event.preventDefault();
        selectTab(tabs[(i + step + tabs.length) % tabs.length], true);
      }
    });
  });
}
