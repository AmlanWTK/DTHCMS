// Metro for a pnpm monorepo.
//
// Expo's config detects the workspace root itself since SDK 52, so most of what a
// monorepo guide tells you to add is already the default. What is not defaulted is
// NativeWind, which compiles the Tailwind classes at bundle time from global.css.
const { getDefaultConfig } = require('expo/metro-config');
const { withNativeWind } = require('nativewind/metro');

const config = getDefaultConfig(__dirname);

module.exports = withNativeWind(config, { input: './src/styles/global.css' });
