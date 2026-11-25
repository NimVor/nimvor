<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Core\Application;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Services\ChangelogService;

/** What changed in the newest releases, in the window that opens from the version number in the top bar. */
final class ChangelogController extends Controller
{
    public function content(Request $request): Response
    {
        $releases = (new ChangelogService(Application::basePath('CHANGELOG.md')))->releases(10);
        return Response::json(['success' => true, 'html' => $this->view->partial('partials/changelog_card', ['releases' => $releases])]);
    }
}
