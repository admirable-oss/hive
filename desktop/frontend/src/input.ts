// Input to a pane goes in the order it was typed. The bridge runs calls
// concurrently, so two calls in flight could reach the pane swapped (seen
// under load: a pasted line with two characters exchanged). Each pane
// therefore has one input call in flight at a time; what is typed
// meanwhile is sent, in order, by the next one.
const inputQueues = new Map<string, { buf: string; running: boolean }>();
const inputChunk = 256 << 10; // well under the hub's 1 MiB limit

export function queueInput(pane: string, data: string, send: (pane: string, data: string) => Promise<void>): Promise<void> {
  let q = inputQueues.get(pane);
  if (!q) {
    q = { buf: "", running: false };
    inputQueues.set(pane, q);
  }
  q.buf += data;
  if (q.running) return Promise.resolve(); // the call in flight sends it
  const queue = q;
  queue.running = true;
  return (async () => {
    try {
      while (queue.buf) {
        const next = queue.buf.slice(0, inputChunk);
        queue.buf = queue.buf.slice(next.length);
        await send(pane, next);
      }
    } catch (err) {
      queue.buf = ""; // keys after a failed one would arrive out of context
      throw err;
    } finally {
      queue.running = false;
      if (!queue.buf) inputQueues.delete(pane);
    }
  })();
}
