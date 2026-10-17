// The one way a browser script waits for an in-place save or post to finish: the page marks <html data-busy> from the
// moment the form is submitted until it shows the answer (the page read again included). A form cannot be watched
// for this, it leaves the page when its dialog closes or its row goes. No fixed sleep: a slow machine just waits longer.
// Usage: const idle = require("./idle.js")(pg); await idle();
module.exports = pg => async () => {
  await pg.waitForFunction(() => !document.documentElement.hasAttribute("data-busy"), { timeout: 60000 });
  // the frame after the last change has been drawn
  await pg.evaluate(() => new Promise(r => requestAnimationFrame(() => requestAnimationFrame(r))));
};
