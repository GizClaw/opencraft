import {
  Flame,
  FolderOpen,
  Globe,
  Lock,
  ShieldCheck,
  Terminal,
} from 'lucide-react';
import type { ComponentType } from 'react';

// Session sandbox modes and the YOLO risk list shared by the composer
// and the General settings tab. Copy lives here (translated through
// i18n keys) so the two surfaces cannot drift.

export interface SessionModeOption {
  value: 'read-only' | 'workspace' | 'yolo';
  labelKey: string;
  bannerKey: string;
  icon: ComponentType<{ className?: string; size?: string | number }>;
}

export const SESSION_MODES: SessionModeOption[] = [
  {
    value: 'read-only',
    labelKey: 'chat.readOnlyMode',
    bannerKey: 'chat.readOnlyBanner',
    icon: Lock,
  },
  {
    value: 'workspace',
    labelKey: 'chat.workspaceMode',
    bannerKey: 'chat.workspaceBanner',
    icon: ShieldCheck,
  },
  {
    value: 'yolo',
    labelKey: 'chat.yoloMode',
    bannerKey: 'chat.yoloBanner',
    icon: Flame,
  },
];

export const YOLO_RISKS: Array<{
  icon: ComponentType<{ className?: string; size?: string | number }>;
  titleKey: string;
  bodyKey: string;
}> = [
  {
    icon: FolderOpen,
    titleKey: 'chat.yoloConfirmFiles',
    bodyKey: 'chat.yoloConfirmFilesBody',
  },
  {
    icon: Terminal,
    titleKey: 'chat.yoloConfirmTerminal',
    bodyKey: 'chat.yoloConfirmTerminalBody',
  },
  {
    icon: Globe,
    titleKey: 'chat.yoloConfirmNetwork',
    bodyKey: 'chat.yoloConfirmNetworkBody',
  },
];
