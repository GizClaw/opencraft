import { expect, test } from '@playwright/test';
import { mockBackend } from './mock/backend';

// Skills page: a reference inside a SKILL.md opens in a preview dialog
// on top of the skill drawer. It used to be routed through the chat's
// link router, which repainted the session's file rail behind the
// settings page.
const SKILLS = [
  {
    name: 'plan',
    description: 'Break tasks into an executable plan.',
    scope: 'user',
    path: '/tmp/.agents/skills/plan/SKILL.md',
  },
];

test('opens SKILL.md references in a dialog above the skill drawer', async ({
  page,
}) => {
  await page.addInitScript(
    mockBackend as never,
    {
      viewerFile: {
        path: '/tmp/.agents/skills/plan/references/deploy.md',
        rel: 'references/deploy.md',
        root: 'skill',
        name: 'deploy.md',
        media_type: 'text/markdown',
        size: 12,
        text: 'Deploy steps',
      },
      handlers: {
        'Settings.Skills': `async () => (${JSON.stringify(SKILLS)})`,
        'Settings.SkillContent': `async () => '# Plan instructions\\n\\n[deploy.md](references/deploy.md) and [docs](https://example.com/guide)'`,
      },
    } as never,
  );
  await page.goto('/');

  await page.getByRole('button', { name: 'Skills' }).click();
  await page
    .getByRole('button', { name: /^plan / })
    .first()
    .click();

  const drawer = page.getByRole('dialog', { name: 'plan' });
  await expect(
    drawer.getByRole('heading', { name: 'Plan instructions' }),
  ).toBeVisible();

  // The reference opens a dialog page of its own, above the drawer.
  await drawer.getByRole('link', { name: 'deploy.md' }).click();

  const dialog = page.getByRole('dialog', { name: 'deploy.md' });
  await expect(dialog).toBeVisible();
  await expect(dialog.getByText('Deploy steps')).toBeVisible();
  await expect(dialog.getByText('references/deploy.md')).toBeVisible();
  await expect(page).toHaveURL('/');

  // Escape closes the dialog and leaves the drawer in place.
  await page.keyboard.press('Escape');
  await expect(dialog).toBeHidden();
  await expect(drawer).toBeVisible();

  // An external reference still goes to the system browser.
  await drawer.getByRole('link', { name: 'docs' }).click();
  await expect
    .poll(() =>
      page.evaluate(
        () => (globalThis as never as { __extUrl?: string }).__extUrl,
      ),
    )
    .toBe('https://example.com/guide');
  await expect(page.getByRole('dialog', { name: 'docs' })).toBeHidden();
});
