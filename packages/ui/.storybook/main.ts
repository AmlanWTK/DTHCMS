import type { StorybookConfig } from '@storybook/react-vite';

const config: StorybookConfig = {
  stories: ['../src/**/*.stories.tsx'],
  addons: [
    '@storybook/addon-essentials',
    // Runs axe against every story as it renders. The same engine as the unit tests, in
    // a real browser rather than jsdom - so unlike the unit run, colour contrast is
    // genuinely checked here.
    '@storybook/addon-a11y',
  ],
  framework: { name: '@storybook/react-vite', options: {} },
  core: { disableTelemetry: true },
  typescript: { reactDocgen: 'react-docgen-typescript' },
};

export default config;
