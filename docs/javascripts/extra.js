// Extra JavaScript for CityList documentation

document.addEventListener('DOMContentLoaded', function() {
  // Add copy button functionality to code blocks
  const codeBlocks = document.querySelectorAll('pre code');

  codeBlocks.forEach(function(codeBlock) {
    const button = document.createElement('button');
    button.className = 'copy-button';
    button.textContent = 'Copy';
    button.style.cssText = 'position: absolute; top: 5px; right: 5px; padding: 4px 8px; font-size: 12px; cursor: pointer; background: #bd93f9; color: #282a36; border: none; border-radius: 3px;';

    const pre = codeBlock.parentElement;
    pre.style.position = 'relative';
    pre.appendChild(button);

    button.addEventListener('click', function() {
      const text = codeBlock.textContent;
      navigator.clipboard.writeText(text).then(function() {
        button.textContent = 'Copied!';
        setTimeout(function() {
          button.textContent = 'Copy';
        }, 2000);
      });
    });
  });

  // Add smooth scrolling to anchor links
  const anchorLinks = document.querySelectorAll('a[href^="#"]');

  anchorLinks.forEach(function(link) {
    link.addEventListener('click', function(e) {
      const targetId = this.getAttribute('href').substring(1);
      const targetElement = document.getElementById(targetId);

      if (targetElement) {
        e.preventDefault();
        targetElement.scrollIntoView({
          behavior: 'smooth',
          block: 'start'
        });
      }
    });
  });

  // Add external link indicator
  const externalLinks = document.querySelectorAll('a[href^="http"]');

  externalLinks.forEach(function(link) {
    if (!link.hostname.includes(window.location.hostname)) {
      link.setAttribute('target', '_blank');
      link.setAttribute('rel', 'noopener noreferrer');

      // Add external link icon
      const icon = document.createElement('span');
      icon.textContent = ' ↗';
      icon.style.fontSize = '0.8em';
      link.appendChild(icon);
    }
  });

  // Console message
  console.log('%cCityList API Documentation', 'color: #bd93f9; font-size: 20px; font-weight: bold;');
  console.log('%cBuilt with MkDocs Material + Dracula Theme', 'color: #8be9fd;');
  console.log('%cGitHub: https://github.com/apimgr/citylist', 'color: #50fa7b;');
});
