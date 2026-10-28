<?php

declare(strict_types=1);

namespace NimVor\Http\Controllers;

use NimVor\Hosting\Scope;
use NimVor\Http\Request;
use NimVor\Http\Response;
use NimVor\Http\Session\SessionInterface;
use NimVor\Services\AuditLogger;
use NimVor\Services\FeatureService;
use NimVor\Support\UserFacingException;
use NimVor\Support\ValidationException;
use NimVor\View\View;

/** Feature Manager: feature lists for hosting plans (administrators). */
final class FeaturesController extends Controller
{
    public function __construct(
        View $view,
        SessionInterface $session,
        private readonly FeatureService $features,
        private readonly AuditLogger $audit,
    ) {
        parent::__construct($view, $session);
    }

    private function denied(Request $request): ?Response
    {
        return Scope::isAll($this->user($request)) ? null : $this->render('errors/error', ['title' => 'Access denied', 'message' => 'Only administrators manage feature lists.', 'status' => 403], 403);
    }

    public function index(Request $request): Response
    {
        return $this->denied($request) ?? $this->render('features/index', ['lists' => $this->features->lists(), 'catalogue' => FeatureService::FEATURES]);
    }

    public function store(Request $request): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        $id = (int) $request->input('list_id', 0);
        try {
            $saved = $this->features->save($id > 0 ? $id : null, $request->all(), $this->userId($request));
        } catch (ValidationException $e) {
            return $this->redirectWithError('/features', implode(' ', $e->errors()));
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/features', $e);
        }
        $this->audit->record('feature_list.save', 'feature_list', (string) $saved, AuditLogger::SUCCESS, ['name' => trim($request->string('name')), 'features' => array_values((array) $request->input('features', []))]);
        return $this->redirectWithSuccess('/features', 'Feature list saved. It applies to the plans using it immediately.');
    }

    public function destroy(Request $request, string $id): Response
    {
        if ($r = $this->denied($request)) {
            return $r;
        }
        try {
            $name = $this->features->delete((int) $id);
        } catch (UserFacingException $e) {
            return $this->redirectWithFailure('/features', $e);
        }
        $this->audit->record('feature_list.delete', 'feature_list', $id, AuditLogger::SUCCESS, ['name' => $name]);
        return $this->redirectWithSuccess('/features', 'Feature list “' . $name . '” removed; plans that used it now have every feature.');
    }
}
