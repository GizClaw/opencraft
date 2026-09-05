import { render, screen } from '@testing-library/react';
import { beforeEach, describe, expect, it } from 'vitest';
import { usePluginStore } from '../store';
import type { SettingsPanelContribution } from '../types';
import { PluginPanels } from './PluginPanels';

function panel(
  id: string,
  title: string,
  tab?: string,
): SettingsPanelContribution {
  return {
    pluginId: 'plug',
    id,
    title,
    order: 10,
    tab,
    Component: () => <span>body</span>,
  };
}

beforeEach(() => {
  usePluginStore.setState({ panels: [] });
});

describe('PluginPanels tab filtering', () => {
  it('renders only panels contributed to the requested tab', () => {
    usePluginStore.setState({
      panels: [
        panel('general-1', 'General panel', 'general'),
        panel('display-1', 'Display panel', 'display'),
        panel('import-1', 'Import source', 'import'),
        panel('drawer-1', 'Drawer panel', 'plugins'),
        panel('default-1', 'Default panel'),
      ],
    });
    render(<PluginPanels tab="general" />);

    expect(screen.getByText('General panel')).toBeInTheDocument();
    expect(screen.queryByText('Display panel')).not.toBeInTheDocument();
    expect(screen.queryByText('Import source')).not.toBeInTheDocument();
    expect(screen.queryByText('Drawer panel')).not.toBeInTheDocument();
    expect(screen.queryByText('Default panel')).not.toBeInTheDocument();
  });

  it('renders display-tab panels on their own surface', () => {
    usePluginStore.setState({
      panels: [
        panel('display-1', 'Display panel', 'display'),
        panel('general-1', 'General panel', 'general'),
      ],
    });
    render(<PluginPanels tab="display" />);

    expect(screen.getByText('Display panel')).toBeInTheDocument();
    expect(screen.queryByText('General panel')).not.toBeInTheDocument();
  });

  it('renders nothing when the tab has no contributions', () => {
    const { container } = render(<PluginPanels tab="import" />);
    expect(container.firstChild).toBeNull();
  });
});
