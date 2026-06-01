# VHS script for the kbark README demo GIF.
#
# Renders docs/demo.gif showing the wedge in 30-40 seconds:
#   1. open kbark on a kind cluster with a CrashLoopBackOff pod
#   2. press ? on cause-crash → AI diagnosis streams
#   3. esc to dismiss → q to quit
#
# Regenerate
# ----------
# Prerequisites
#   - vhs:                   `brew install charmbracelet/tap/vhs` (or see vhs.charm.sh)
#   - the kbark binary:      `task build` (writes ./bin/kbark)
#   - a kind cluster with a cause-crash pod that panics on startup
#     (see CONTRIBUTING.md for the test-fixtures recipe — M11 will
#     formalise this; for now you can recreate from the user notes)
#   - ANTHROPIC_API_KEY set in your environment (or whichever
#     provider your active profile uses)
#
# Run
#   ANTHROPIC_API_KEY=$ANTHROPIC_API_KEY vhs docs/demo.tape
#
# The output lands at docs/demo.gif. Commit it alongside this script.
# Target: <5MB. If the file blows past that, drop the FontSize or
# tighten the Sleep timings rather than dropping content — the
# wedge (streaming diagnosis + tool-call breadcrumb) is what sells.

Output docs/demo.gif

# Frame
Set Width  1100
Set Height 640
Set Padding 24
Set FontSize 14
# JetBrains Mono is the safest cross-platform monospace; vhs falls
# back to its bundled font if not installed.
Set FontFamily "JetBrains Mono"
# Catppuccin is high-contrast enough for GitHub's light/dark previews
# without losing kbark's brand-accent cyan.
Set Theme "Catppuccin Mocha"
Set Shell "bash"

# Open kbark. The first-paint should be near-instant post-M9.6 (async
# Switch), so the user sees the headers + namespace before the rows
# arrive.
Type "./bin/kbark"
Enter
Sleep 1200ms

# Show the pod table. Cursor lands on the first row (bad-image); move
# down to cause-crash, the CrashLoopBackOff pod that's the star of
# the demo.
Down
Sleep 600ms

# Press the wedge key. The diagnose modal opens with the pod context
# already loaded; the model streams its answer and may call a tool
# (get_previous_logs) which surfaces as a ⚙ breadcrumb.
Type "?"
# Wait for the streamed diagnosis to converge. Anthropic typically
# returns in 4-8s for our payload; 12s is a generous ceiling.
Sleep 12s

# Dismiss and quit cleanly so the alt-screen tear-down is in the GIF.
Escape
Sleep 800ms
Type "q"
Sleep 400ms
