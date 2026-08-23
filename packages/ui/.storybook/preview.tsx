import type { Decorator, Preview } from '@storybook/react';

import '@dthcms/design-tokens/css';
import '@dthcms/design-tokens/print';
import '../src/styles.css';
import './storybook.css';

import { LanguageProvider } from '../src/lib/language.js';

/**
 * Theme and language are toolbar globals rather than story arguments.
 *
 * Every story has to render in four combinations — light and dark, English and Bangla —
 * and the failures the checkpoint cares about only appear in three of them. Making the
 * axes global means a reviewer flips two switches and sees all four, instead of every
 * story author remembering to write four variants and most of them not.
 */

const withTheme: Decorator = (Story, context) => {
  const theme = context.globals.theme ?? 'light';
  const language = context.globals.language ?? 'en';

  // The attribute goes on the document element because that is where the token
  // stylesheet looks for it. Setting it on a wrapper would style the story and leave
  // Storybook's own chrome mismatched, which makes dark mode hard to judge.
  document.documentElement.setAttribute('data-theme', theme);

  return (
    <LanguageProvider language={language}>
      <div className="sb-canvas">
        <Story />
      </div>
    </LanguageProvider>
  );
};

const preview: Preview = {
  globalTypes: {
    theme: {
      description: 'Colour theme',
      defaultValue: 'light',
      toolbar: {
        title: 'Theme',
        icon: 'circlehollow',
        items: [
          { value: 'light', title: 'Light' },
          { value: 'dark', title: 'Dark' },
        ],
        dynamicTitle: true,
      },
    },
    language: {
      description: 'Interface language',
      defaultValue: 'en',
      toolbar: {
        title: 'Language',
        icon: 'globe',
        items: [
          { value: 'en', title: 'English' },
          { value: 'bn', title: 'বাংলা' },
        ],
        dynamicTitle: true,
      },
    },
  },
  decorators: [withTheme],
  parameters: {
    controls: { expanded: true },
    a11y: {
      // Fail the story rather than merely annotating it. An accessibility panel nobody
      // opens is an accessibility check nobody runs.
      test: 'error',
    },
    backgrounds: { disable: true },
  },
};

export default preview;
