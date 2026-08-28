// Hello plugin bundle for the OpenCraft plugin host (Phase 0).
// The host evaluates this file as `new Function('ctx', src)`; plugins
// build components with ctx.React and declare their contribution
// points through ctx.contribute.
(function (ctx) {
  var React = ctx.React;
  var storage = ctx.capabilities.storage;
  var ui = ctx.capabilities.ui;

  function readCounter(setValue) {
    storage.get('counter').then(function (v) {
      setValue(v === null ? '0' : v);
    });
  }

  function incrementCounter() {
    return storage
      .get('counter')
      .then(function (v) {
        var n = String((parseInt(v || '0', 10) || 0) + 1);
        return storage.set('counter', n).then(function () {
          return n;
        });
      })
      .then(function (n) {
        ui.flash('Hello counter: ' + n);
        return n;
      });
  }

  function HelloPanel() {
    var state = React.useState('…');
    var value = state[0];
    var setValue = state[1];
    React.useEffect(function () {
      readCounter(setValue);
    }, []);

    return React.createElement(
      'div',
      { className: 'flex flex-col gap-2 text-sm text-fg' },
      React.createElement('p', null, 'Counter: ', value),
      React.createElement(
        'button',
        {
          onClick: function () {
            incrementCounter().then(function () {
              readCounter(setValue);
            });
          },
          className: 'w-fit rounded-md bg-accent px-2 py-1 text-xs text-white hover:opacity-90',
        },
        'Increment (capabilities.storage)'
      )
    );
  }

  function StatusLabel() {
    return React.createElement(
      'span',
      { className: 'text-dim' },
      'Hello'
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
          ui.flash('Hello from the hello plugin!');
        },
      },
    ],
    commands: [
      {
        id: 'hello-increment',
        title: 'Hello: increment counter',
        order: 10,
        run: function () {
          incrementCounter();
        },
      },
    ],
    statusBar: [
      { id: 'hello-status', order: 10, Component: StatusLabel },
    ],
  });
})(ctx);
