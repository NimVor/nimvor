<?php

declare(strict_types=1);

namespace NimVor\Core;

use Closure;
use ReflectionClass;
use ReflectionNamedType;
use RuntimeException;

/**
 * Minimal dependency-injection container.
 *
 * Services are registered as factories and shared (created once). Classes
 * that are not registered are autowired through their constructor types.
 */
final class Container
{
    /** @var array<string, Closure(Container): object> */
    private array $factories = [];

    /** @var array<string, object> */
    private array $instances = [];

    /** @var array<string, true> */
    private array $resolving = [];

    /**
     * @param Closure(Container): object $factory
     */
    public function set(string $id, Closure $factory): void
    {
        $this->factories[$id] = $factory;
        unset($this->instances[$id]);
    }

    public function instance(string $id, object $object): void
    {
        $this->instances[$id] = $object;
    }

    public function has(string $id): bool
    {
        return isset($this->instances[$id]) || isset($this->factories[$id]) || class_exists($id);
    }

    /**
     * @template T of object
     * @param class-string<T>|string $id
     * @return ($id is class-string<T> ? T : object)
     */
    public function get(string $id): object
    {
        if (isset($this->instances[$id])) {
            return $this->instances[$id];
        }
        if (isset($this->resolving[$id])) {
            throw new RuntimeException("Circular dependency while resolving {$id}");
        }
        $this->resolving[$id] = true;
        try {
            $object = isset($this->factories[$id])
                ? ($this->factories[$id])($this)
                : $this->autowire($id);
        } finally {
            unset($this->resolving[$id]);
        }
        $this->instances[$id] = $object;
        return $object;
    }

    private function autowire(string $class): object
    {
        if (!class_exists($class)) {
            throw new RuntimeException("Service {$class} is not registered");
        }
        $ref = new ReflectionClass($class);
        if (!$ref->isInstantiable()) {
            throw new RuntimeException("Service {$class} is not instantiable");
        }
        $ctor = $ref->getConstructor();
        if ($ctor === null) {
            return $ref->newInstance();
        }
        $args = [];
        foreach ($ctor->getParameters() as $param) {
            $type = $param->getType();
            if ($type instanceof ReflectionNamedType && !$type->isBuiltin()) {
                $args[] = $this->get($type->getName());
                continue;
            }
            if ($param->isDefaultValueAvailable()) {
                $args[] = $param->getDefaultValue();
                continue;
            }
            throw new RuntimeException("Cannot autowire parameter \${$param->getName()} of {$class}");
        }
        return $ref->newInstanceArgs($args);
    }
}
