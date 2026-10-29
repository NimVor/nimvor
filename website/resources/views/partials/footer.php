<?php /** @var \NimVorSite\Core\View $v */ ?>
<footer class="site-footer">
    <div class="container">
        <div class="footer-grid">
            <div>
                <a class="brand" href="/"><img src="<?= e($v->asset('img/logo-dark.png')) ?>" width="44" height="44" alt="">NimVor</a>
                <p class="mt-2 maxw-34">A free, open-source control panel for Linux servers. Every privileged change is explicit, validated, audited and reversible.</p>
            </div>
            <div>
                <h4>Product</h4>
                <ul>
                    <li><a href="/features">Features</a></li>
                    <li><a href="/docs">Documentation</a></li>
                    <li><a href="/get-started">Install</a></li>
                    <li><a href="https://github.com/NimVor/nimvor/releases" rel="noopener">Releases</a></li>
                </ul>
            </div>
            <div>
                <h4>Resources</h4>
                <ul>
                    <li><a href="/resources/blog">Blog</a></li>
                    <li><a href="/resources/community">Community &amp; source</a></li>
                    <li><a href="/resources/build-services">Build services</a></li>
                    <li><a href="/resources/sponsors">Sponsors</a></li>
                    <li><a href="/resources/problems">Problems</a></li>
                    <li><a href="/resources/stats">Statistics</a></li>
                    <li><a href="/resources/about">About us</a></li>
                </ul>
            </div>
            <div>
                <h4>Support</h4>
                <ul>
                    <li><a href="/support/contact">Contact us</a></li>
                    <li><a href="/support/privacy">Privacy policy</a></li>
                    <li><a href="/docs/security-policy">Report a vulnerability</a></li>
                    <li><a href="/login">Log in</a></li>
                </ul>
            </div>
        </div>
        <div class="footer-bottom">
            <span>&copy; <?= gmdate('Y') ?> The NimVor authors. NimVor is released under the MIT licence.</span>
            <span>A product of <a href="https://whasys.com" rel="noopener">WhaSys</a></span>
        </div>
    </div>
</footer>
