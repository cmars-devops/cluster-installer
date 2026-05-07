import { addMessages, init, getLocaleFromNavigator } from 'svelte-i18n';
import ko from './ko.json';
import en from './en.json';

addMessages('ko', ko);
addMessages('en', en);

init({
  fallbackLocale: 'en',
  initialLocale: getLocaleFromNavigator() ?? 'ko'
});
