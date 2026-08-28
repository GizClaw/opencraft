// Hello plugin for the OpenCraft plugin host.
//
// The protocol IS Cordis: the host loads this ES module, assembles it
// into a Cordis plugin ({ name, inject, apply }) and mounts it with
// ctx.plugin(). Plugins register through the injected services and
// the context primitives (ctx.on / ctx.effect / contribution
// registrars); everything is reversible and torn down with the
// plugin's scope.
export const name = 'hello';
export const inject = ['storage', 'react'];

export function apply(ctx) {
  var React = ctx.react;
  var storage = ctx.storage;
  var ui = ctx.ui;

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
          className:
            'w-fit rounded-md bg-accent px-2 py-1 text-xs text-white hover:opacity-90',
        },
        'Increment (ctx.storage)'
      )
    );
  }

  function StatusLabel() {
    return React.createElement('span', { className: 'text-dim' }, 'Hello');
  }

  // Contribution registrars are services: add() returns a disposer
  // tied to this plugin's scope (removed automatically on disable).
  ctx.settingsPanels.add({
    id: 'hello-panel',
    title: 'Hello',
    order: 10,
    Component: HelloPanel,
  });
  ctx.sidebarEntries.add({
    id: 'hello-entry',
    title: 'Hello',
    order: 10,
    onClick: function () {
      ui.flash('Hello from the hello plugin!');
    },
  });
  ctx.commands.add({
    id: 'hello-increment',
    title: 'Hello: increment counter',
    order: 10,
    run: function () {
      incrementCounter();
    },
  });
  ctx.statusBar.add({
    id: 'hello-status',
    order: 10,
    Component: StatusLabel,
  });

  // ctx.on is the Cordis event primitive; subscriptions are removed
  // with the plugin scope. Host UI events are forwarded by the host.
  ctx.on('turn_end', function () {
    // Host UI events flow through the shared Cordis event bus.
  });
}
