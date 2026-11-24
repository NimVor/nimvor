<?php /** @var \NimVorSite\Core\View $v */ ?>
<footer class="site-footer">
    <div class="container">
        <div class="footer-grid">
            <div>
                <a class="brand" href="/"><img src="<?= e($v->asset('img/logo-dark.png')) ?>" width="44" height="44" alt="">NimVor</a>
                <p class="mt-2 maxw-34">A free, open-source control panel for Linux servers. Every privileged change is explicit, validated, audited and reversible.</p>
                <?php $links = $siteLinks ?? ['social' => [], 'trustpilot' => '']; ?>
                <?php if (!empty($links['social'])): ?>
                    <ul class="social" aria-label="NimVor on social media">
                        <?php foreach (\NimVorSite\Core\App::SOCIAL as $key => $label): if (empty($links['social'][$key])) { continue; } ?>
                            <li><a href="<?= e($links['social'][$key]) ?>" target="_blank" rel="noopener noreferrer me" aria-label="NimVor on <?= e($label) ?>" title="<?= e($label) ?>"><?= $v->icon($key, 20) ?></a></li>
                        <?php endforeach; ?>
                    </ul>
                <?php endif; ?>
                <?php if (!empty($links['trustpilot'])): ?>
                    <a class="trustpilot-link" href="<?= e($links['trustpilot']) ?>" target="_blank" rel="noopener noreferrer"><?= $v->icon('star', 18) ?>Review us on Trustpilot</a>
                <?php endif; ?>
            </div>
            <div>
                <h4>Product</h4>
                <ul>
                    <li><a href="/features">Features</a></li>
                    <li><a href="/docs">Documentation</a></li>
                    <li><a href="/api-reference">API reference</a></li>
                    <li><a href="/get-started">Install</a></li>
                    <li><a href="/resources/releases">Releases</a></li>
                </ul>
            </div>
            <div>
                <h4>Resources</h4>
                <ul>
                    <li><a href="/resources/blog">Blog</a></li>
                    <li><a href="/resources/build-services">Build services</a></li>
                    <li><a href="/resources/videos">Videos</a></li>
                    <li><a href="/reviews">Reviews</a></li>
                    <li><a href="/resources/problems">Problems</a></li>
                    <li><a href="/resources/stats">Statistics</a></li>
                </ul>
            </div>
            <div>
                <h4>Support</h4>
                <ul>
                    <li><a href="/support/contact">Contact us</a></li>
                    <li><a href="/support/privacy">Privacy policy</a></li>
                    <li><a href="/resources/community">Community &amp; source</a></li>
                    <li><a href="/resources/sponsors">Sponsors</a></li>
                    <li><a href="/docs/security-policy">Report a vulnerability</a></li>
                    <li><a href="/resources/about">About us</a></li>
                    <li><a href="/login">Log in</a></li>
                </ul>
            </div>
        </div>
        <div class="footer-bottom">
            <span>&copy; Copyright <?= gmdate('Y') ?> NimVor</span>
            <span>Powered by <a href="https://tplix.dev" target="_blank" rel="noopener noreferrer">TPLix</a> &middot; Hosted on <a href="https://nimvor.com" target="_blank" rel="noopener noreferrer">NimVor</a></span>
        </div>
    </div>
</footer>
