/* 首屏前应用主题，避免深色用户看到白色闪屏。键名与 app.js 共用。 */
(function () {
  var key = 'workbuddy.ui.theme';
  var mode = 'system';
  try {
    var saved = localStorage.getItem(key);
    if (saved === 'light' || saved === 'dark' || saved === 'system') mode = saved;
  } catch (e) {}
  var resolved = mode;
  if (mode === 'system') {
    try {
      resolved = window.matchMedia('(prefers-color-scheme: dark)').matches ? 'dark' : 'light';
    } catch (e) {
      resolved = 'light';
    }
  }
  document.documentElement.dataset.theme = resolved;
  document.documentElement.dataset.themeMode = mode;
})();
