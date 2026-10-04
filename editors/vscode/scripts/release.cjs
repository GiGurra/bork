'use strict';

function releaseVersion(refType, refName, version) {
  if (refType !== 'tag' || refName !== `vscode-v${version}`) {
    throw new Error(`Release from tag vscode-v${version}, matching package.json (got ${refType} ${refName})`);
  }
  return version;
}

if (require.main === module) {
  const manifest = require('../package.json');
  try {
    console.log(releaseVersion(process.env.GITHUB_REF_TYPE, process.env.GITHUB_REF_NAME, manifest.version));
  } catch (error) {
    console.error(error.message);
    process.exitCode = 1;
  }
}
module.exports = { releaseVersion };
