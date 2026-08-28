// Hello plugin bundle for the OpenCraft plugin host (Phase 0).
// The host evaluates this file as `new Function('ctx', src)`; plugins
// build components with ctx.React and declare their contribution
// points through ctx.contribute.
(function (ctx) {
  var React = ctx.React;

  function HelloPanel() {
    return React.createElement(
      'div',
      { className: 'text-sm text-fg' },
      'Hello from the hello plugin!',
      React.createElement(
        'p',
        { className: 'mt-1 text-xs text-dim' },
        'This panel is contributed via the settingsPanels contribution point.'
      )
    );
  }

  ctx.contribute({
    settingsPanels: [
      { id: 'hello-panel', title: 'Hello', order: 10, Component: HelloPanel },
    ],
    sidebarEntries: [
      {
        id: 'hello-entry',
        title: 'Hello',
        order: 10,
        onClick: function () {
          ctx.ui.flash('Hello from the hello plugin!');
        },
      },
    ],
  });
})(ctx);
