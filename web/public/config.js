// Settings the web app reads at run time (S14.2). The nginx image answers
// /config.js itself, from its RVAV_WEB_* environment; this empty one is for
// the development server and builds served without it.
window.__RVAV_CONFIG__ = window.__RVAV_CONFIG__ || {};
