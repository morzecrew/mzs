// Builds the "mzs (Seti)" file icon theme: VS Code's own Seti theme, verbatim, with one
// entry added for .mzs.
//
// VS Code icon themes have no inheritance — a theme is either the whole mapping or
// nothing — so the only way to keep Seti's icons for every other file is to copy its
// table and patch ours in. This script does that copy, so refreshing after a VS Code
// update is one command rather than a merge by hand:
//
//     node editors/vscode/icons/theme/build.js
//     node editors/vscode/icons/theme/build.js /path/to/vscode/resources/app
//
// Seti is MIT (Microsoft's packaging) over MIT (jesseweed/seti-ui); ThirdPartyNotices.txt
// next to this file is copied along with it.

const fs = require('fs');
const path = require('path');

const HERE = __dirname;

// Where VS Code keeps the theme on each platform. The first one that exists wins; a
// path may also be passed as an argument.
const CANDIDATES = [
  process.argv[2] && path.join(process.argv[2], 'extensions/theme-seti'),
  '/usr/share/code/resources/app/extensions/theme-seti',
  '/usr/share/code-insiders/resources/app/extensions/theme-seti',
  '/opt/visual-studio-code/resources/app/extensions/theme-seti',
  '/snap/code/current/usr/share/code/resources/app/extensions/theme-seti',
  '/Applications/Visual Studio Code.app/Contents/Resources/app/extensions/theme-seti',
  path.join(process.env.LOCALAPPDATA || '', 'Programs/Microsoft VS Code/resources/app/extensions/theme-seti'),
].filter(Boolean);

const source = CANDIDATES.find(p => fs.existsSync(path.join(p, 'icons/vs-seti-icon-theme.json')));
if (!source) {
  console.error('could not find the Seti theme inside VS Code. Name the path:\n' +
    '  node build.js /path/to/vscode/resources/app');
  process.exit(1);
}

const seti = JSON.parse(fs.readFileSync(path.join(source, 'icons/vs-seti-icon-theme.json'), 'utf8'));

// The icons are images rather than glyphs of Seti's font: the logo is not in that font,
// and an image definition is the supported way to bring your own (fontColor does not
// apply to it, which is why there are two files).
seti.iconDefinitions._mzs = { iconPath: '../mzs-dark.svg' };
seti.iconDefinitions._mzs_light = { iconPath: '../mzs-light.svg' };

seti.fileExtensions.mzs = '_mzs';
seti.languageIds.mzs = '_mzs';
seti.light = seti.light || {};
seti.light.fileExtensions = seti.light.fileExtensions || {};
seti.light.languageIds = seti.light.languageIds || {};
seti.light.fileExtensions.mzs = '_mzs_light';
seti.light.languageIds.mzs = '_mzs_light';

seti.information_for_contributors = [
  'GENERATED: editors/vscode/icons/theme/build.js. Do not edit by hand.',
  'This is VS Code\'s Seti theme (MIT, Microsoft; based on MIT, jesseweed/seti-ui) plus one entry for .mzs.',
  'Refresh after a VS Code update: node editors/vscode/icons/theme/build.js',
  ...(Array.isArray(seti.information_for_contributors) ? seti.information_for_contributors : []),
];

fs.writeFileSync(path.join(HERE, 'mzs-seti-icon-theme.json'), JSON.stringify(seti, null, 1) + '\n');

// The font the copied table refers to, and the notice that must travel with it.
fs.copyFileSync(path.join(source, 'icons/seti.woff'), path.join(HERE, 'seti.woff'));
fs.copyFileSync(path.join(source, 'ThirdPartyNotices.txt'), path.join(HERE, 'ThirdPartyNotices.txt'));

const exts = Object.keys(seti.fileExtensions).length;
console.log(`built from ${source}`);
console.log(`file extensions: ${exts}, icon definitions: ${Object.keys(seti.iconDefinitions).length}`);
