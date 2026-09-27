<?php

namespace App;

use Doctrine\DBAL\Connection;
use Doctrine\DBAL\DriverManager;
use Doctrine\DBAL\Tools\DsnParser;

/**
 * Plain DBAL, no ORM: the example is about the callback handler, not the mapping.
 */
final class ConnectionFactory
{
    public static function create(string $url): Connection
    {
        return DriverManager::getConnection((new DsnParser())->parse($url));
    }
}
